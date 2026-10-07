package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/azeno/xdocker/internal/dockerapi"
	"github.com/azeno/xdocker/internal/jobs"
	"github.com/azeno/xdocker/internal/match"
	"github.com/azeno/xdocker/internal/store"
)

// ImageRow is one tag of a local image (an untagged image gets one row
// with Dangling set and its id as Ref).
type ImageRow struct {
	ID       string   `json:"id"`
	ShortID  string   `json:"shortId"`
	Ref      string   `json:"ref"`
	Repo     string   `json:"repo"`
	Tag      string   `json:"tag"`
	Digest   string   `json:"digest"`
	Created  int64    `json:"created"`
	Size     int64    `json:"size"`
	UsedBy   []string `json:"usedBy"`
	Running  bool     `json:"running"`
	Dangling bool     `json:"dangling"`
	TagCount int      `json:"tagCount"` // tags sharing this image id
}

// ImageFilter narrows the image list.
type ImageFilter struct {
	Q        string
	Unused   bool
	Dangling bool
	Before   int64 // created before (unix seconds), 0 = any
	After    int64
	MinSize  int64
}

// Images lists local images, one row per tag, newest first.
func (a *App) Images(ctx context.Context, f ImageFilter) ([]ImageRow, error) {
	m, err := match.Search(f.Q)
	if err != nil {
		return nil, err
	}
	rows, err := a.allImages(ctx)
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, r := range rows {
		if !m(r.Ref) && !(r.Dangling && m(r.ShortID)) {
			continue
		}
		if f.Unused && len(r.UsedBy) > 0 || f.Dangling && !r.Dangling ||
			f.Before > 0 && r.Created >= f.Before || f.After > 0 && r.Created <= f.After || f.MinSize > 0 && r.Size < f.MinSize {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (a *App) allImages(ctx context.Context) ([]ImageRow, error) {
	imgs, err := a.Docker.Images(ctx)
	if err != nil {
		return nil, err
	}
	cs, err := a.Docker.Containers(ctx)
	if err != nil {
		return nil, err
	}
	used := map[string][]string{}
	running := map[string]bool{}
	for _, c := range cs {
		used[c.ImageID] = append(used[c.ImageID], c.Name())
		if c.State == "running" || c.State == "restarting" || c.State == "paused" {
			running[c.ImageID] = true
		}
	}
	var rows []ImageRow
	for _, im := range imgs {
		short := strings.TrimPrefix(im.ID, "sha256:")
		if len(short) > 12 {
			short = short[:12]
		}
		digest := ""
		if len(im.RepoDigests) > 0 {
			if _, d, ok := strings.Cut(im.RepoDigests[0], "@"); ok {
				digest = d
			}
		}
		base := ImageRow{ID: im.ID, ShortID: short, Digest: digest, Created: im.Created, Size: im.Size,
			UsedBy: used[im.ID], Running: running[im.ID]}
		if base.UsedBy == nil {
			base.UsedBy = []string{}
		}
		tags := realTags(im.RepoTags)
		if len(tags) == 0 {
			r := base
			r.Ref, r.Repo, r.Tag, r.Dangling = im.ID, "<none>", "<none>", true
			rows = append(rows, r)
			continue
		}
		for _, t := range tags {
			r := base
			r.Ref = t
			r.Repo, r.Tag = dockerapi.SplitRef(t)
			r.TagCount = len(tags)
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Created != rows[j].Created {
			return rows[i].Created > rows[j].Created
		}
		return rows[i].Ref < rows[j].Ref
	})
	return rows, nil
}

func realTags(ts []string) []string {
	var out []string
	for _, t := range ts {
		if t != "<none>:<none>" && t != "" {
			out = append(out, t)
		}
	}
	return out
}

// DeletePlanItem is one row of a bulk delete preview.
type DeletePlanItem struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest,omitempty"`
	Size   int64  `json:"size"`
	Action string `json:"action"` // delete | skip
	Reason string `json:"reason"`
	// AlsoRemoves lists other tags that disappear too (registry: same digest).
	AlsoRemoves []string `json:"alsoRemoves,omitempty"`
}

// DeletePlan is a bulk delete preview.
type DeletePlan struct {
	Items       []DeletePlanItem `json:"items"`
	DeleteCount int              `json:"deleteCount"`
	SkipCount   int              `json:"skipCount"`
	Bytes       int64            `json:"bytes"`
}

// PreviewImageDelete builds the plan for deleting refs. Images used by a
// running container are always skipped; images used only by stopped
// containers are skipped unless force is set. Bytes counts an image only
// when all of its tags are selected (otherwise only a tag is removed).
func (a *App) PreviewImageDelete(ctx context.Context, refs []string, force bool) (DeletePlan, error) {
	rows, err := a.allImages(ctx)
	if err != nil {
		return DeletePlan{}, err
	}
	byRef := map[string]ImageRow{}
	tagsOf := map[string]int{}
	for _, r := range rows {
		byRef[r.Ref] = r
		tagsOf[r.ID]++
	}
	selected := map[string]int{}
	seen := map[string]bool{}
	var plan DeletePlan
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		r, ok := byRef[ref]
		it := DeletePlanItem{Ref: ref, Size: r.Size, Action: "delete"}
		switch {
		case !ok:
			it.Action, it.Reason = "skip", "not found"
		case r.Running:
			it.Action, it.Reason = "skip", "used by running container "+strings.Join(r.UsedBy, ", ")
		case len(r.UsedBy) > 0 && !force:
			it.Action, it.Reason = "skip", "used by stopped container "+strings.Join(r.UsedBy, ", ")
		case len(r.UsedBy) > 0:
			it.Reason = "force: used by stopped container " + strings.Join(r.UsedBy, ", ")
		}
		if it.Action == "delete" {
			selected[r.ID]++
			plan.DeleteCount++
		} else {
			plan.SkipCount++
		}
		plan.Items = append(plan.Items, it)
	}
	for id, n := range selected {
		if n == tagsOf[id] {
			for _, r := range rows {
				if r.ID == id {
					plan.Bytes += r.Size
					break
				}
			}
		}
	}
	for i := range plan.Items {
		it := &plan.Items[i]
		if r, ok := byRef[it.Ref]; ok && it.Action == "delete" && selected[r.ID] < tagsOf[r.ID] {
			if it.Reason == "" {
				it.Reason = "only the tag is removed; the image keeps other tags"
			} else {
				it.Reason += " (only the tag is removed)"
			}
			it.Size = 0
		}
	}
	return plan, nil
}

// StartImageDelete runs a bulk delete as a background job.
func (a *App) StartImageDelete(ctx context.Context, user string, refs []string, force bool) (int64, error) {
	plan, err := a.PreviewImageDelete(ctx, refs, force)
	if err != nil {
		return 0, err
	}
	if plan.DeleteCount == 0 {
		return 0, fmt.Errorf("nothing to delete: all %d selected images are skipped", plan.SkipCount)
	}
	title := fmt.Sprintf("Delete %d images", plan.DeleteCount)
	return a.Jobs.Start(store.Job{Type: "bulk_delete", Title: title, User: user, Total: len(plan.Items)},
		func(ctx context.Context, r *jobs.Run) (string, error) {
			deleted, failed := 0, 0
			for _, it := range plan.Items {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				ji := store.JobItem{Target: "local", Ref: it.Ref, Action: it.Action, Reason: it.Reason, Bytes: it.Size}
				if it.Action == "skip" {
					ji.Result = "skipped"
					r.Item(ji)
					continue
				}
				if err := a.Docker.ImageRemove(ctx, it.Ref, force); err != nil {
					ji.Result, ji.Error = "error", err.Error()
					failed++
					r.Logf("%s: %v", it.Ref, err)
				} else {
					ji.Result = "deleted"
					deleted++
					r.Logf("deleted %s", it.Ref)
				}
				r.Item(ji)
			}
			summary := fmt.Sprintf("%d deleted, %d skipped, %d failed, %s freed", deleted, plan.SkipCount, failed, HumanBytes(r.Snapshot().Bytes))
			a.Audit(ctx, user, "image.bulk_delete", fmt.Sprintf("%d images", deleted), map[string]any{"job": r.ID(), "force": force}, nil)
			return summary, nil
		}, nil)
}

// PullImage pulls ref, using stored credentials when ref points at a
// configured registry.
func (a *App) PullImage(ctx context.Context, user, ref string) (int64, error) {
	auth := a.authForRef(ctx, ref)
	return a.Jobs.Start(store.Job{Type: "pull", Title: "Pull " + ref, User: user, Total: 1},
		func(ctx context.Context, r *jobs.Run) (string, error) {
			last := ""
			err := a.Docker.ImagePull(ctx, ref, auth, func(p dockerapi.Progress) {
				line := strings.TrimSpace(p.ID + " " + p.Status)
				if line != last && p.Progress == "" {
					r.Logf("%s", line)
					last = line
				}
			})
			a.Audit(ctx, user, "image.pull", ref, nil, err)
			if err != nil {
				return "", err
			}
			r.Item(store.JobItem{Target: "local", Ref: ref, Action: "pull", Result: "done"})
			return "pulled " + ref, nil
		}, nil)
}

// PushImage pushes ref (which must carry the registry host).
func (a *App) PushImage(ctx context.Context, user, ref string) (int64, error) {
	auth := a.authForRef(ctx, ref)
	return a.Jobs.Start(store.Job{Type: "push", Title: "Push " + ref, User: user, Total: 1},
		func(ctx context.Context, r *jobs.Run) (string, error) {
			err := a.Docker.ImagePush(ctx, ref, auth, func(p dockerapi.Progress) {
				if p.Progress == "" {
					r.Logf("%s", strings.TrimSpace(p.ID+" "+p.Status))
				}
			})
			a.Audit(ctx, user, "image.push", ref, nil, err)
			if err != nil {
				return "", err
			}
			r.Item(store.JobItem{Target: "local", Ref: ref, Action: "push", Result: "done"})
			return "pushed " + ref, nil
		}, nil)
}

// TagImage adds a tag ("host:5050/repo:tag").
func (a *App) TagImage(ctx context.Context, user, src, target string) error {
	repo, tag := dockerapi.SplitRef(target)
	err := a.Docker.ImageTag(ctx, src, repo, tag)
	a.Audit(ctx, user, "image.tag", src+" -> "+target, nil, err)
	return err
}

// PruneImages removes dangling images.
func (a *App) PruneImages(ctx context.Context, user string) (int64, error) {
	n, err := a.Docker.ImagesPrune(ctx)
	a.Audit(ctx, user, "image.prune", "dangling images", map[string]any{"reclaimed": n}, err)
	return n, err
}

func (a *App) authForRef(ctx context.Context, ref string) *dockerapi.RegistryAuth {
	regs, err := a.Store.Registries(ctx)
	if err != nil {
		return nil
	}
	for _, g := range regs {
		host := hostOf(g.URL)
		if host != "" && strings.HasPrefix(ref, host+"/") {
			pass, _ := a.Box.Open(g.PasswordEnc)
			return &dockerapi.RegistryAuth{Username: g.Username, Password: pass, ServerAddress: host}
		}
	}
	return nil
}

func hostOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	h, _, _ := strings.Cut(u, "/")
	return h
}
