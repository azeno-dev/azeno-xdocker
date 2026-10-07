package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/azeno/xdocker/internal/compose"
	"github.com/azeno/xdocker/internal/jobs"
	"github.com/azeno/xdocker/internal/store"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// StackRow is a stack as shown in the UI.
type StackRow struct {
	Name       string   `json:"name"`
	HasFolder  bool     `json:"hasFolder"`
	Dir        string   `json:"dir"`
	Running    int      `json:"running"`
	Total      int      `json:"total"`
	Containers []string `json:"containers"`
	ModTime    int64    `json:"modTime"`
	Status     string   `json:"status"` // running | partial | stopped | not deployed
	WorkingDir string   `json:"workingDir"`
}

// Stacks lists stack folders and compose projects seen on containers
// (including projects created by Container Manager).
func (a *App) Stacks(ctx context.Context) ([]StackRow, error) {
	rows := map[string]*StackRow{}
	if list, err := a.Compose.List(); err == nil {
		for _, s := range list {
			rows[s.Name] = &StackRow{Name: s.Name, HasFolder: true, Dir: s.Dir, ModTime: s.ModTime}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	cs, err := a.Docker.Containers(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		p := c.Labels[ProjectLabel]
		if p == "" {
			continue
		}
		r, ok := rows[p]
		if !ok {
			r = &StackRow{Name: p}
			rows[p] = r
		}
		r.Total++
		if c.State == "running" {
			r.Running++
		}
		r.Containers = append(r.Containers, c.Name())
		if wd := c.Labels["com.docker.compose.project.working_dir"]; wd != "" {
			r.WorkingDir = wd
		}
	}
	out := make([]StackRow, 0, len(rows))
	for _, r := range rows {
		switch {
		case r.Total == 0:
			r.Status = "not deployed"
		case r.Running == r.Total:
			r.Status = "running"
		case r.Running == 0:
			r.Status = "stopped"
		default:
			r.Status = "partial"
		}
		if r.Containers == nil {
			r.Containers = []string{}
		}
		sort.Strings(r.Containers)
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// StackPortConflicts checks the host ports a compose file publishes.
func (a *App) StackPortConflicts(ctx context.Context, name, yaml, env string) (map[string][]PortConflict, error) {
	out := map[string][]PortConflict{}
	for _, p := range compose.PublishedPorts(yaml, compose.ParseEnv(env)) {
		cs, err := a.CheckPort(ctx, p.Port, p.Proto, name)
		if err != nil {
			return nil, err
		}
		if len(cs) > 0 {
			out[fmt.Sprintf("%d/%s", p.Port, p.Proto)] = cs
		}
	}
	return out, nil
}

// StartStackAction runs a compose action as a job. "up" checks ports first
// unless force is set; "pull" is followed by "up".
func (a *App) StartStackAction(ctx context.Context, user, name, action string, force bool) (int64, error) {
	if _, ok := compose.Actions[action]; !ok {
		return 0, fmt.Errorf("unknown action %q", action)
	}
	if action == "up" || action == "pull" {
		_, y, env, err := a.Compose.Get(name)
		if err != nil {
			return 0, fmt.Errorf("stack %s has no compose file in %s", name, a.Cfg.StacksDir)
		}
		if !force {
			conflicts, err := a.StackPortConflicts(ctx, name, y, env)
			if err != nil {
				return 0, err
			}
			if len(conflicts) > 0 {
				var parts []string
				for port, cs := range conflicts {
					parts = append(parts, port+" used by "+cs[0].Name)
				}
				sort.Strings(parts)
				return 0, &PortConflictError{Conflicts: conflicts, Msg: "port conflict: " + strings.Join(parts, "; ")}
			}
		}
	}
	if (action == "down" || action == "down-volumes" || action == "stop") && name == a.selfStack(ctx) {
		return 0, errors.New("xDocker will not stop its own stack")
	}
	return a.Jobs.Start(store.Job{Type: "stack", Title: fmt.Sprintf("Stack %s: %s", name, action), User: user, Total: 1},
		func(ctx context.Context, r *jobs.Run) (string, error) {
			steps := []string{action}
			if action == "pull" {
				steps = []string{"pull", "up"}
			}
			for _, st := range steps {
				r.Logf("$ compose %s", st)
				out, err := a.Compose.Run(ctx, name, st)
				for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
					if l != "" {
						r.Logf("%s", l)
					}
				}
				if err != nil {
					a.Audit(ctx, user, "stack."+action, name, nil, err)
					return "", err
				}
			}
			r.Item(store.JobItem{Target: "stack", Ref: name, Action: action, Result: "done"})
			a.Audit(ctx, user, "stack."+action, name, nil, nil)
			return "stack " + name + ": " + action + " done", nil
		}, nil)
}

// PortConflictError carries details for the UI.
type PortConflictError struct {
	Conflicts map[string][]PortConflict
	Msg       string
}

func (e *PortConflictError) Error() string { return e.Msg }

func (a *App) selfStack(ctx context.Context) string {
	cs, err := a.Docker.Containers(ctx)
	if err != nil {
		return ""
	}
	for _, c := range cs {
		if c.Labels[SelfLabel] == "true" {
			return c.Labels[ProjectLabel]
		}
	}
	return ""
}

// DeleteStack runs `down` and removes the folder when removeFiles is set.
func (a *App) DeleteStack(ctx context.Context, user, name string, removeFiles bool) error {
	if name == a.selfStack(ctx) {
		return errors.New("xDocker will not delete its own stack")
	}
	if _, err := a.Compose.Run(ctx, name, "down"); err != nil {
		a.Audit(ctx, user, "stack.delete", name, nil, err)
		return err
	}
	var err error
	if removeFiles {
		err = a.Compose.Remove(name)
	}
	a.Audit(ctx, user, "stack.delete", name, map[string]any{"removeFiles": removeFiles}, err)
	return err
}
