// Package jobs runs long operations (bulk deletes, retention runs, garbage
// collection, stack deploys) in the background and records their progress
// and per-item results in the store.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/azeno/xdocker/internal/store"
)

// Runner starts jobs. At most `parallel` jobs run at once; the rest queue.
type Runner struct {
	st  *store.Store
	sem chan struct{}
	wg  sync.WaitGroup

	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
	base    context.Context
}

// New creates a runner whose jobs stop when ctx is cancelled.
func New(ctx context.Context, st *store.Store, parallel int) *Runner {
	if parallel < 1 {
		parallel = 1
	}
	return &Runner{st: st, sem: make(chan struct{}, parallel), cancels: map[int64]context.CancelFunc{}, base: ctx}
}

// Run is handed to a job function to report progress.
type Run struct {
	st      *store.Store
	mu      sync.Mutex
	job     store.Job
	log     strings.Builder
	pending []store.JobItem
	lastSav time.Time
}

// ID returns the job id.
func (r *Run) ID() int64 { return r.job.ID }

// Logf appends a line to the job log.
func (r *Run) Logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(&r.log, "%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	r.saveLocked(false)
}

// SetTotal sets the number of steps.
func (r *Run) SetTotal(n int) {
	r.mu.Lock()
	r.job.Total = n
	r.saveLocked(true)
	r.mu.Unlock()
}

// Item records one finished item and advances progress.
func (r *Run) Item(it store.JobItem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	it.JobID = r.job.ID
	r.pending = append(r.pending, it)
	r.job.Done++
	if it.Result == "deleted" || it.Result == "freed" {
		r.job.Bytes += it.Bytes
	}
	r.saveLocked(false)
}

// AddBytes adds reclaimed bytes not tied to an item (e.g. GC).
func (r *Run) AddBytes(n int64) {
	r.mu.Lock()
	r.job.Bytes += n
	r.mu.Unlock()
}

// Snapshot returns the current job state.
func (r *Run) Snapshot() store.Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	j := r.job
	j.Log = r.log.String()
	return j
}

func (r *Run) saveLocked(force bool) {
	if !force && time.Since(r.lastSav) < 500*time.Millisecond {
		return
	}
	r.flushLocked()
}

func (r *Run) flushLocked() {
	r.lastSav = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if len(r.pending) > 0 {
		if err := r.st.AddJobItems(ctx, r.pending); err != nil {
			slog.Error("save job items", "job", r.job.ID, "err", err)
		}
		r.pending = nil
	}
	j := r.job
	j.Log = r.log.String()
	if err := r.st.UpdateJob(ctx, j); err != nil {
		slog.Error("save job", "job", r.job.ID, "err", err)
	}
}

// Func is the body of a job. It returns a one-line summary.
type Func func(ctx context.Context, r *Run) (summary string, err error)

// Start queues a job and returns its id immediately. done, when non-nil, is
// called with the final job state.
func (rn *Runner) Start(meta store.Job, fn Func, done func(store.Job)) (int64, error) {
	id, err := rn.st.CreateJob(context.Background(), meta)
	if err != nil {
		return 0, err
	}
	meta.ID, meta.Status = id, "queued"
	ctx, cancel := context.WithCancel(rn.base)
	rn.mu.Lock()
	rn.cancels[id] = cancel
	rn.mu.Unlock()

	rn.wg.Add(1)
	go func() {
		defer rn.wg.Done()
		defer func() {
			rn.mu.Lock()
			delete(rn.cancels, id)
			rn.mu.Unlock()
			cancel()
		}()
		select {
		case rn.sem <- struct{}{}:
		case <-ctx.Done():
			meta.Status, meta.FinishedAt, meta.Summary = "cancelled", time.Now().Unix(), "cancelled before start"
			rn.st.UpdateJob(context.Background(), meta)
			return
		}
		defer func() { <-rn.sem }()

		r := &Run{st: rn.st, job: meta}
		r.mu.Lock()
		r.job.Status, r.job.StartedAt = "running", time.Now().Unix()
		r.saveLocked(true)
		r.mu.Unlock()

		summary, err := safeCall(ctx, r, fn)

		r.mu.Lock()
		r.job.FinishedAt = time.Now().Unix()
		switch {
		case err != nil && ctx.Err() != nil:
			r.job.Status = "cancelled"
			r.job.Summary = "cancelled"
		case err != nil:
			r.job.Status = "failed"
			r.job.Summary = err.Error()
			fmt.Fprintf(&r.log, "%s  ERROR: %v\n", time.Now().Format("15:04:05"), err)
		default:
			r.job.Status = "done"
			r.job.Summary = summary
		}
		r.flushLocked()
		final := r.job
		final.Log = r.log.String()
		r.mu.Unlock()
		slog.Info("job finished", "id", id, "type", meta.Type, "status", final.Status, "summary", final.Summary)
		if done != nil {
			done(final)
		}
	}()
	return id, nil
}

func safeCall(ctx context.Context, r *Run, fn Func) (s string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("internal error: %v", p)
		}
	}()
	return fn(ctx, r)
}

// Cancel stops a queued or running job.
func (rn *Runner) Cancel(id int64) bool {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if c, ok := rn.cancels[id]; ok {
		c()
		return true
	}
	return false
}

// Running reports how many jobs are queued or running.
func (rn *Runner) Running() int {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	return len(rn.cancels)
}

// Wait blocks until all jobs have returned.
func (rn *Runner) Wait() { rn.wg.Wait() }
