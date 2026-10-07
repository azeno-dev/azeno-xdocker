package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/azeno/xdocker/internal/jobs"
	"github.com/azeno/xdocker/internal/match"
	"github.com/azeno/xdocker/internal/notify"
	"github.com/azeno/xdocker/internal/retention"
	"github.com/azeno/xdocker/internal/store"
)

// CronParser accepts standard 5-field cron expressions (and @daily etc.).
var CronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ValidatePolicy checks a policy before saving.
func (a *App) ValidatePolicy(ctx context.Context, p *store.Policy) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return errors.New("name is required")
	}
	switch p.Target {
	case "local":
	case "registry", "both":
		if p.RegistryID == 0 {
			return errors.New("choose a registry")
		}
		if _, err := a.Store.Registry(ctx, p.RegistryID); err != nil {
			return errors.New("registry not found")
		}
	default:
		return fmt.Errorf("unknown target %q", p.Target)
	}
	if _, err := CronParser.Parse(p.Cron); err != nil {
		return fmt.Errorf("schedule: %w", err)
	}
	if p.Timezone != "" {
		if _, err := time.LoadLocation(p.Timezone); err != nil {
			return fmt.Errorf("unknown time zone %q", p.Timezone)
		}
	}
	rp := toEngine(*p, time.UTC)
	return rp.Validate()
}

func toEngine(p store.Policy, loc *time.Location) retention.Policy {
	return retention.Policy{
		// Repository matching is done by the caller (see matchRepo), so
		// local images pulled from a registry host also match "azeno/*".
		TagInclude: p.TagInclude, TagExclude: p.TagExclude,
		Period: retention.Period(p.Period), KeepPerPeriod: p.KeepPerPeriod, KeepMode: p.KeepMode,
		KeepLastN: p.KeepLastN,
		MinAge:    time.Duration(p.MinAgeDays) * 24 * time.Hour,
		MaxAge:    time.Duration(p.MaxAgeDays) * 24 * time.Hour,
		SkipInUse: p.SkipInUse, DateSource: p.DateSource, DateRegex: p.DateRegex, DateLayout: p.DateLayout,
		Location: loc,
	}
}

// matchRepo matches a repository against the policy pattern, with and
// without a leading registry host ("registry.nas.lan:5050/azeno/api").
func matchRepo(m match.Matcher, repo string) bool {
	if m(repo) {
		return true
	}
	if first, rest, ok := strings.Cut(repo, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return m(rest)
	}
	return false
}

// PolicyPreview is the plan for one policy.
type PolicyPreview struct {
	Local        *retention.Result `json:"local,omitempty"`
	Registry     *retention.Result `json:"registry,omitempty"`
	RegistryName string            `json:"registryName,omitempty"`
	Warnings     []string          `json:"warnings"`
	GeneratedAt  int64             `json:"generatedAt"`
}

// PreviewPolicy computes what a policy would keep and delete right now.
// The policy does not need to be saved.
func (a *App) PreviewPolicy(ctx context.Context, p store.Policy) (PolicyPreview, error) {
	if err := a.ValidatePolicy(ctx, &p); err != nil {
		return PolicyPreview{}, err
	}
	now := time.Now()
	loc := a.Location(ctx, p.Timezone)
	ep := toEngine(p, loc)
	repoM, _ := match.Pattern(p.RepoPattern)
	pv := PolicyPreview{Warnings: []string{}, GeneratedAt: now.Unix()}

	if p.Target == "local" || p.Target == "both" {
		rows, err := a.allImages(ctx)
		if err != nil {
			return pv, err
		}
		var items []retention.Item
		for _, r := range rows {
			if r.Dangling || !matchRepo(repoM, r.Repo) {
				continue
			}
			items = append(items, retention.Item{Repo: r.Repo, Tag: r.Tag, Digest: r.ID, Created: time.Unix(r.Created, 0), Size: r.Size, UsedBy: r.UsedBy})
		}
		res, err := retention.Plan(ep, items, now)
		if err != nil {
			return pv, err
		}
		countLocalBytes(&res, rows)
		pv.Local = &res
	}
	if p.Target == "registry" || p.Target == "both" {
		c, g, err := a.RegistryClient(ctx, p.RegistryID)
		if err != nil {
			return pv, err
		}
		pv.RegistryName = g.Name
		repos, err := c.Catalog(ctx)
		if err != nil {
			return pv, fmt.Errorf("registry %s: %w", g.Name, err)
		}
		var items []retention.Item
		for _, repo := range repos {
			if !repoM(repo) {
				continue
			}
			tags, err := c.Tags(ctx, repo)
			if err != nil {
				return pv, err
			}
			for _, ti := range c.TagInfos(ctx, repo, tags) {
				if ti.Error != "" {
					pv.Warnings = append(pv.Warnings, fmt.Sprintf("%s:%s skipped: %s", repo, ti.Tag, ti.Error))
					continue
				}
				if ti.Created.IsZero() && p.DateSource != "tag" {
					pv.Warnings = append(pv.Warnings, fmt.Sprintf("%s:%s has no creation date; it is treated as new and kept", repo, ti.Tag))
					ti.Created = now
				}
				items = append(items, retention.Item{Repo: repo, Tag: ti.Tag, Digest: ti.Digest, Created: ti.Created, Size: ti.Size})
			}
		}
		res, err := retention.Plan(ep, items, now)
		if err != nil {
			return pv, err
		}
		protectSharedDigests(&res)
		pv.Registry = &res
	}
	return pv, nil
}

// countLocalBytes counts an image's size only when every one of its tags
// (also those outside the policy's scope) is deleted; removing some tags
// of an image frees nothing.
func countLocalBytes(res *retention.Result, rows []ImageRow) {
	tags := map[string]int{}
	for _, r := range rows {
		tags[r.ID]++
	}
	deleted := map[string]int{}
	for _, d := range res.Decisions {
		if d.Action == retention.Delete {
			deleted[d.Digest]++
		}
	}
	counted := map[string]bool{}
	res.DeleteSize = 0
	for i := range res.Decisions {
		d := &res.Decisions[i]
		if d.Action != retention.Delete {
			continue
		}
		if deleted[d.Digest] < tags[d.Digest] || counted[d.Digest] {
			if deleted[d.Digest] < tags[d.Digest] {
				d.Reason += " (tag only: the image has other tags)"
			}
			d.Size = 0
			continue
		}
		counted[d.Digest] = true
		res.DeleteSize += d.Size
	}
}

// protectSharedDigests turns "delete" into "keep" when another kept tag of
// the same repository points to the same digest: deleting the manifest
// would remove the kept tag too.
func protectSharedDigests(res *retention.Result) {
	kept := map[string]string{}
	for _, d := range res.Decisions {
		if d.Action == retention.Keep && d.Digest != "" {
			kept[d.Repo+"@"+d.Digest] = d.Tag
		}
	}
	for i := range res.Decisions {
		d := &res.Decisions[i]
		if d.Action != retention.Delete {
			continue
		}
		if tag, ok := kept[d.Repo+"@"+d.Digest]; ok {
			d.Action = retention.Keep
			d.Reason = "same digest as kept tag " + tag
			res.DeleteCount--
			res.DeleteSize -= d.Size
			res.KeepCount++
		}
	}
}

// StartPolicyRun runs a policy as a job. dryRun forces a report-only run;
// otherwise the policy's own dry-run setting applies.
func (a *App) StartPolicyRun(ctx context.Context, user string, id int64, dryRun bool) (int64, error) {
	p, err := a.Store.Policy(ctx, id)
	if err != nil {
		return 0, err
	}
	dry := dryRun || p.DryRun
	a.mu.Lock()
	if a.policyRunning[id] {
		a.mu.Unlock()
		return 0, fmt.Errorf("policy %q is already running", p.Name)
	}
	a.policyRunning[id] = true
	a.mu.Unlock()
	release := func() {
		a.mu.Lock()
		delete(a.policyRunning, id)
		a.mu.Unlock()
	}
	title := "Retention: " + p.Name
	if dry {
		title += " (dry run)"
	}
	jobID, err := a.Jobs.Start(store.Job{Type: "retention", Title: title, PolicyID: id, User: user, DryRun: dry},
		func(ctx context.Context, r *jobs.Run) (string, error) {
			defer release()
			return a.runPolicy(ctx, r, p, dry, user)
		},
		func(j store.Job) {
			if p.NotifyEmail || p.NotifyWebhook {
				m := notify.Message{
					Title:  fmt.Sprintf("%s: %s", title, j.Status),
					Text:   fmt.Sprintf("%s\n\nJob #%d · %d items · %s reclaimed", j.Summary, j.ID, j.Done, HumanBytes(j.Bytes)),
					Status: j.Status,
					Details: map[string]any{"job": j.ID, "policy": p.Name, "dryRun": dry, "bytes": j.Bytes},
				}
				a.Notify(context.Background(), p.NotifyEmail, p.NotifyWebhook, m)
			}
		})
	if err != nil {
		release()
		return 0, err
	}
	a.Store.MarkPolicyRun(ctx, id, jobID)
	return jobID, nil
}

func (a *App) runPolicy(ctx context.Context, r *jobs.Run, p store.Policy, dry bool, user string) (string, error) {
	r.Logf("computing plan for %q", p.Name)
	pv, err := a.PreviewPolicy(ctx, p)
	if err != nil {
		return "", err
	}
	for _, w := range pv.Warnings {
		r.Logf("warning: %s", w)
	}
	total := 0
	if pv.Local != nil {
		total += len(pv.Local.Decisions)
	}
	if pv.Registry != nil {
		total += len(pv.Registry.Decisions)
	}
	r.SetTotal(total)
	deleted, failed, kept := 0, 0, 0

	if pv.Local != nil {
		running := map[string]bool{}
		if rows, err := a.allImages(ctx); err == nil {
			for _, row := range rows {
				if row.Running {
					running[row.Ref] = true
				}
			}
		}
		for _, d := range pv.Local.Decisions {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			ji := store.JobItem{Target: "local", Ref: d.Ref, Digest: d.Digest, Action: string(d.Action), Reason: d.Reason, Bytes: d.Size}
			switch {
			case d.Action == retention.Keep:
				ji.Result = "kept"
				kept++
			case running[d.Ref]:
				ji.Result, ji.Reason = "skipped", "used by a running container"
			case dry:
				ji.Result = "dry-run"
			default:
				if err := a.Docker.ImageRemove(ctx, d.Ref, false); err != nil {
					ji.Result, ji.Error = "error", err.Error()
					failed++
				} else {
					ji.Result = "deleted"
					deleted++
					r.Logf("deleted local %s (%s)", d.Ref, d.Reason)
				}
			}
			r.Item(ji)
		}
	}

	var gcErr error
	var gcFreed int64
	if pv.Registry != nil {
		c, g, err := a.RegistryClient(ctx, p.RegistryID)
		if err != nil {
			return "", err
		}
		done := map[string]error{}
		regDeleted := 0
		for _, d := range pv.Registry.Decisions {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			ji := store.JobItem{Target: "registry:" + g.Name, Ref: d.Ref, Digest: d.Digest, Action: string(d.Action), Reason: d.Reason, Bytes: d.Size}
			switch {
			case d.Action == retention.Keep:
				ji.Result = "kept"
				kept++
			case dry:
				ji.Result = "dry-run"
			default:
				key := d.Repo + "@" + d.Digest
				err, seen := done[key]
				if !seen {
					err = c.DeleteManifest(ctx, d.Repo, d.Digest)
					done[key] = err
				} else {
					ji.Bytes = 0
				}
				if err != nil {
					ji.Result, ji.Error = "error", err.Error()
					failed++
				} else {
					ji.Result = "deleted"
					deleted++
					regDeleted++
					r.Logf("deleted %s:%s (%s)", g.Name, d.Ref, d.Reason)
				}
			}
			r.Item(ji)
		}
		if p.GCAfter && !dry && regDeleted > 0 {
			gcFreed, gcErr = a.runGC(ctx, r, g)
		}
	}

	var s string
	if dry {
		toDelete := 0
		if pv.Local != nil {
			toDelete += pv.Local.DeleteCount
		}
		if pv.Registry != nil {
			toDelete += pv.Registry.DeleteCount
		}
		s = fmt.Sprintf("dry run: would delete %d, keep %d", toDelete, kept)
	} else {
		s = fmt.Sprintf("%d deleted, %d kept, %d failed, %s freed", deleted, kept, failed, HumanBytes(r.Snapshot().Bytes-gcFreed))
		if gcErr != nil {
			s += "; garbage collection failed: " + gcErr.Error()
		} else if gcFreed > 0 {
			s += "; garbage collection freed " + HumanBytes(gcFreed)
		}
	}
	a.Audit(ctx, user, "retention.run", p.Name, map[string]any{"job": r.ID(), "dryRun": dry, "deleted": deleted}, nil)
	return s, nil
}

// NextRuns returns the next n run times of a cron expression.
func NextRuns(spec string, loc *time.Location, n int) []time.Time {
	sched, err := CronParser.Parse(spec)
	if err != nil {
		return nil
	}
	var out []time.Time
	t := time.Now().In(loc)
	for i := 0; i < n; i++ {
		t = sched.Next(t)
		out = append(out, t)
	}
	return out
}
