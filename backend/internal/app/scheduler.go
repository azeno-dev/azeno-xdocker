package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// Scheduler runs enabled retention policies on their cron schedules and
// prunes old job history daily.
type Scheduler struct {
	a       *App
	mu      sync.Mutex
	c       *cron.Cron
	entries map[int64]cron.EntryID
	reload  chan struct{}
}

// NewScheduler creates a scheduler; call Run to start it.
func NewScheduler(a *App) *Scheduler {
	s := &Scheduler{a: a, entries: map[int64]cron.EntryID{}, reload: make(chan struct{}, 1)}
	a.OnPoliciesChanged(s.Reload)
	return s
}

// Reload asks the scheduler to re-read policies.
func (s *Scheduler) Reload() {
	select {
	case s.reload <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is done.
func (s *Scheduler) Run(ctx context.Context) {
	s.apply(ctx)
	housekeeping := time.NewTicker(24 * time.Hour)
	defer housekeeping.Stop()
	s.prune(ctx)
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			if s.c != nil {
				<-s.c.Stop().Done()
			}
			s.mu.Unlock()
			return
		case <-s.reload:
			s.apply(ctx)
		case <-housekeeping.C:
			s.prune(ctx)
		}
	}
}

func (s *Scheduler) prune(ctx context.Context) {
	st, err := s.a.GetSettings(ctx)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -st.JobHistoryDays)
	if err := s.a.Store.PruneJobs(ctx, cutoff); err != nil {
		slog.Warn("prune jobs", "err", err)
	}
}

func (s *Scheduler) apply(ctx context.Context) {
	ps, err := s.a.Store.Policies(ctx)
	if err != nil {
		slog.Error("scheduler: load policies", "err", err)
		return
	}
	c := cron.New(cron.WithParser(CronParser), cron.WithChain(cron.Recover(cron.DefaultLogger)))
	entries := map[int64]cron.EntryID{}
	for _, p := range ps {
		if !p.Enabled {
			continue
		}
		loc := s.a.Location(ctx, p.Timezone)
		sched, err := CronParser.Parse(p.Cron)
		if err != nil {
			slog.Warn("scheduler: bad cron", "policy", p.Name, "err", err)
			continue
		}
		id := p.ID
		name := p.Name
		eid := c.Schedule(inLocation{sched, loc}, cron.FuncJob(func() {
			jobID, err := s.a.StartPolicyRun(context.Background(), "scheduler", id, false)
			if err != nil {
				slog.Warn("scheduled policy run", "policy", name, "err", err)
				return
			}
			slog.Info("scheduled policy run started", "policy", name, "job", jobID)
		}))
		entries[id] = eid
	}
	s.mu.Lock()
	old := s.c
	s.c, s.entries = c, entries
	s.mu.Unlock()
	if old != nil {
		old.Stop()
	}
	c.Start()
	slog.Info("scheduler loaded", "policies", len(entries))
}

// NextRun returns the next run of a policy, or zero when not scheduled.
func (s *Scheduler) NextRun(policyID int64) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c == nil {
		return time.Time{}
	}
	if id, ok := s.entries[policyID]; ok {
		return s.c.Entry(id).Next
	}
	return time.Time{}
}

// inLocation evaluates a schedule in a fixed time zone, so "0 3 * * 0"
// means 03:00 in the policy's zone, including across DST changes.
type inLocation struct {
	cron.Schedule
	loc *time.Location
}

func (s inLocation) Next(t time.Time) time.Time { return s.Schedule.Next(t.In(s.loc)) }
