package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/azeno/xdocker/internal/dockerapi"
)

// ContainerRow is a container as shown in the UI.
type ContainerRow struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Image   string           `json:"image"`
	ImageID string           `json:"imageId"`
	State   string           `json:"state"`
	Status  string           `json:"status"`
	Health  string           `json:"health"`
	Created int64            `json:"created"`
	Ports   []dockerapi.Port `json:"ports"`
	Stack   string           `json:"stack"`
	Self    bool             `json:"self"`
}

// ProjectLabel is the compose project label (used by Container Manager too).
const ProjectLabel = "com.docker.compose.project"

// SelfLabel marks xDocker's own container.
const SelfLabel = "xdocker.self"

// Containers lists containers.
func (a *App) Containers(ctx context.Context) ([]ContainerRow, error) {
	cs, err := a.Docker.Containers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ContainerRow, 0, len(cs))
	for _, c := range cs {
		r := ContainerRow{
			ID: c.ID, Name: c.Name(), Image: c.Image, ImageID: c.ImageID, State: c.State, Status: c.Status,
			Created: c.Created, Ports: c.Ports, Stack: c.Labels[ProjectLabel], Self: c.Labels[SelfLabel] == "true",
		}
		switch {
		case strings.Contains(c.Status, "(healthy)"):
			r.Health = "healthy"
		case strings.Contains(c.Status, "(unhealthy)"):
			r.Health = "unhealthy"
		case strings.Contains(c.Status, "(health: starting)"):
			r.Health = "starting"
		}
		if r.Ports == nil {
			r.Ports = []dockerapi.Port{}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// BulkResult is the outcome of one item of a synchronous bulk action.
type BulkResult struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Error string `json:"error,omitempty"`
}

// ContainerBulk runs an action on several containers. xDocker refuses to
// stop, kill or remove its own container.
func (a *App) ContainerBulk(ctx context.Context, user string, ids []string, action string, removeVolumes bool) ([]BulkResult, error) {
	cs, err := a.Containers(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]ContainerRow{}
	for _, c := range cs {
		byID[c.ID] = c
	}
	var out []BulkResult
	for _, id := range ids {
		c, ok := byID[id]
		res := BulkResult{ID: id, Name: c.Name}
		switch {
		case !ok:
			res.Error = "not found"
		case c.Self && action != "start" && action != "restart":
			res.Error = "xDocker will not " + action + " its own container"
		case action == "remove":
			err = a.Docker.ContainerRemove(ctx, id, true, removeVolumes)
		default:
			err = a.Docker.ContainerAction(ctx, id, action)
		}
		if res.Error == "" && err != nil {
			res.Error = err.Error()
		}
		var resErr error
		if res.Error != "" {
			resErr = fmt.Errorf("%s", res.Error)
		}
		a.Audit(ctx, user, "container."+action, c.Name, nil, resErr)
		out = append(out, res)
		err = nil
	}
	return out, nil
}

// Stats is a computed resource sample.
type Stats struct {
	CPUPercent float64 `json:"cpuPercent"`
	MemUsage   int64   `json:"memUsage"`
	MemLimit   int64   `json:"memLimit"`
	NetRx      int64   `json:"netRx"`
	NetTx      int64   `json:"netTx"`
	BlockRead  int64   `json:"blockRead"`
	BlockWrite int64   `json:"blockWrite"`
	Pids       int64   `json:"pids"`
}

// ContainerStats returns CPU/memory/network usage.
func (a *App) ContainerStats(ctx context.Context, id string) (Stats, error) {
	raw, err := a.Docker.ContainerStats(ctx, id)
	if err != nil {
		return Stats{}, err
	}
	return ComputeStats(raw)
}

// ComputeStats turns a raw stats sample into percentages and totals.
func ComputeStats(raw []byte) (Stats, error) {
	var s struct {
		CPU struct {
			Usage struct {
				Total uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			System uint64 `json:"system_cpu_usage"`
			Online int    `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPU struct {
			Usage struct {
				Total uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			System uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		Mem struct {
			Usage int64            `json:"usage"`
			Limit int64            `json:"limit"`
			Stats map[string]int64 `json:"stats"`
		} `json:"memory_stats"`
		Networks map[string]struct {
			Rx int64 `json:"rx_bytes"`
			Tx int64 `json:"tx_bytes"`
		} `json:"networks"`
		Blkio struct {
			Recursive []struct {
				Op    string `json:"op"`
				Value int64  `json:"value"`
			} `json:"io_service_bytes_recursive"`
		} `json:"blkio_stats"`
		Pids struct {
			Current int64 `json:"current"`
		} `json:"pids_stats"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return Stats{}, err
	}
	var out Stats
	cpuDelta := float64(s.CPU.Usage.Total) - float64(s.PreCPU.Usage.Total)
	sysDelta := float64(s.CPU.System) - float64(s.PreCPU.System)
	online := s.CPU.Online
	if online == 0 {
		online = 1
	}
	if cpuDelta > 0 && sysDelta > 0 {
		out.CPUPercent = cpuDelta / sysDelta * float64(online) * 100
	}
	cache := s.Mem.Stats["inactive_file"]
	if cache == 0 {
		cache = s.Mem.Stats["total_inactive_file"]
	}
	out.MemUsage = s.Mem.Usage - cache
	if out.MemUsage < 0 {
		out.MemUsage = s.Mem.Usage
	}
	out.MemLimit = s.Mem.Limit
	for _, n := range s.Networks {
		out.NetRx += n.Rx
		out.NetTx += n.Tx
	}
	for _, b := range s.Blkio.Recursive {
		switch strings.ToLower(b.Op) {
		case "read":
			out.BlockRead += b.Value
		case "write":
			out.BlockWrite += b.Value
		}
	}
	out.Pids = s.Pids.Current
	return out, nil
}

// ContainerTTY reports whether a container was created with a TTY (logs are
// then not multiplexed).
func (a *App) ContainerTTY(ctx context.Context, id string) bool {
	cfg, err := a.Docker.ContainerConfigOf(ctx, id)
	return err == nil && cfg.Config.Tty
}
