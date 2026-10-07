package app

import (
	"context"
	"log/slog"
	"os"

	"github.com/azeno/xdocker/internal/store"
)

// Dashboard is the summary shown on the first page.
type Dashboard struct {
	Docker struct {
		Connected  bool   `json:"connected"`
		Version    string `json:"version"`
		APIVersion string `json:"apiVersion"`
		Error      string `json:"error,omitempty"`
		Host       string `json:"host"`
	} `json:"docker"`
	Containers struct {
		Total   int `json:"total"`
		Running int `json:"running"`
	} `json:"containers"`
	Images struct {
		Total    int   `json:"total"`
		Dangling int   `json:"dangling"`
		Unused   int   `json:"unused"`
		Size     int64 `json:"size"`
	} `json:"images"`
	Stacks     int         `json:"stacks"`
	Registries int         `json:"registries"`
	Policies   int         `json:"policies"`
	Disk       DiskSummary `json:"disk"`
	RecentJobs []store.Job `json:"recentJobs"`
	Conflicts  int         `json:"portConflicts"`
	Version    string      `json:"version"`
}

// DiskSummary condenses `docker system df`.
type DiskSummary struct {
	Images      int64 `json:"images"`
	Containers  int64 `json:"containers"`
	Volumes     int64 `json:"volumes"`
	BuildCache  int64 `json:"buildCache"`
	Reclaimable int64 `json:"reclaimable"` // images not used by any container
}

// GetDashboard collects the summary; parts that fail are left empty.
func (a *App) GetDashboard(ctx context.Context) Dashboard {
	var d Dashboard
	d.Version = a.Cfg.Version
	d.Docker.Host = a.Cfg.DockerHost
	v, err := a.DockerStatus()
	if err == nil && v.Version != "" {
		d.Docker.Connected, d.Docker.Version, d.Docker.APIVersion = true, v.Version, v.APIVersion
	} else if err != nil {
		d.Docker.Error = err.Error()
	} else {
		d.Docker.Error = "connecting…"
	}
	if cs, err := a.Docker.Containers(ctx); err == nil {
		d.Containers.Total = len(cs)
		for _, c := range cs {
			if c.State == "running" {
				d.Containers.Running++
			}
		}
	}
	if rows, err := a.allImages(ctx); err == nil {
		ids := map[string]bool{}
		for _, r := range rows {
			if !ids[r.ID] {
				ids[r.ID] = true
				d.Images.Size += r.Size
				if len(r.UsedBy) == 0 {
					d.Images.Unused++
				}
			}
			if r.Dangling {
				d.Images.Dangling++
			}
		}
		d.Images.Total = len(ids)
	}
	if df, err := a.Docker.SystemDF(ctx); err == nil {
		for _, im := range df.Images {
			if im.Containers == 0 {
				d.Disk.Reclaimable += im.Size - im.SharedSize
			}
		}
		d.Disk.Images = df.LayersSize
		for _, c := range df.Containers {
			d.Disk.Containers += c.SizeRw
		}
		for _, v := range df.Volumes {
			if v.UsageData.Size > 0 {
				d.Disk.Volumes += v.UsageData.Size
			}
		}
		for _, b := range df.BuildCache {
			d.Disk.BuildCache += b.Size
		}
	}
	if ss, err := a.Stacks(ctx); err == nil {
		d.Stacks = len(ss)
	}
	if rs, err := a.Store.Registries(ctx); err == nil {
		d.Registries = len(rs)
	}
	if ps, err := a.Store.Policies(ctx); err == nil {
		for _, p := range ps {
			if p.Enabled {
				d.Policies++
			}
		}
	}
	if pv, err := a.Ports(ctx); err == nil {
		for _, p := range pv.Ports {
			if p.Status == "conflict" {
				d.Conflicts++
			}
		}
	}
	d.RecentJobs, _ = a.Store.Jobs(ctx, 6)
	if d.RecentJobs == nil {
		d.RecentJobs = []store.Job{}
	}
	return d
}

// Bootstrap adds a registry from environment variables on first start
// (XDOCKER_REGISTRY_URL, _USER, _PASSWORD, _CONTAINER, _NAME).
func (a *App) Bootstrap(ctx context.Context) {
	u := os.Getenv("XDOCKER_REGISTRY_URL")
	if u == "" {
		return
	}
	regs, err := a.Store.Registries(ctx)
	if err != nil {
		return
	}
	for _, g := range regs {
		if g.URL == u {
			return
		}
	}
	name := os.Getenv("XDOCKER_REGISTRY_NAME")
	if name == "" {
		name = "nas-registry"
	}
	enc, _ := a.Box.Seal(os.Getenv("XDOCKER_REGISTRY_PASSWORD"))
	_, err = a.Store.SaveRegistry(ctx, store.Registry{Name: name, URL: u, Username: os.Getenv("XDOCKER_REGISTRY_USER"),
		PasswordEnc: enc, Container: os.Getenv("XDOCKER_REGISTRY_CONTAINER"), Managed: os.Getenv("XDOCKER_REGISTRY_CONTAINER") != ""})
	if err != nil {
		slog.Warn("bootstrap registry", "err", err)
	} else {
		slog.Info("registry added from environment", "url", u)
	}
}
