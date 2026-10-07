package app

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/azeno/xdocker/internal/ports"
)

// PortRow is one published port.
type PortRow struct {
	HostIP        string `json:"hostIp"`
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Proto         string `json:"proto"`
	Container     string `json:"container"`
	ContainerID   string `json:"containerId"`
	State         string `json:"state"`
	Stack         string `json:"stack"`
	Status        string `json:"status"` // ok | conflict | shared | local
	Conflict      string `json:"conflict,omitempty"`
}

// PortsView is the Ports page.
type PortsView struct {
	Ports    []PortRow        `json:"ports"`
	Reserved []ports.Reserved `json:"reserved"`
	// HostListeners are listening sockets not owned by a container
	// (nil when the host's /proc is not available).
	HostListeners []ports.Listener `json:"hostListeners"`
	HostChecked   bool             `json:"hostChecked"`
}

// Ports lists every published port and flags conflicts: two containers
// (running or not) claiming the same host port, or a container claiming a
// DSM-reserved port.
func (a *App) Ports(ctx context.Context) (PortsView, error) {
	cs, err := a.Containers(ctx)
	if err != nil {
		return PortsView{}, err
	}
	s, err := a.GetSettings(ctx)
	if err != nil {
		return PortsView{}, err
	}
	reserved, _ := ports.ParseReserved(s.ReservedPorts)
	var rows []PortRow
	seen := map[string]bool{}
	for _, c := range cs {
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue
			}
			k := fmt.Sprintf("%s/%d/%d/%s", c.ID, p.PublicPort, p.PrivatePort, p.Type)
			if seen[k] { // docker lists IPv4 and IPv6 bindings separately
				continue
			}
			seen[k] = true
			rows = append(rows, PortRow{HostIP: p.IP, HostPort: p.PublicPort, ContainerPort: p.PrivatePort, Proto: p.Type,
				Container: c.Name, ContainerID: c.ID, State: c.State, Stack: c.Stack, Status: "ok"})
		}
	}
	// Stopped containers do not report ports in /containers/json; read
	// their port bindings from inspect so conflicts with them show up.
	for _, c := range cs {
		if c.State == "running" {
			continue
		}
		for _, b := range a.stoppedBindings(ctx, c.ID) {
			k := fmt.Sprintf("%s/%d/%d/%s", c.ID, b.HostPort, b.ContainerPort, b.Proto)
			if seen[k] {
				continue
			}
			seen[k] = true
			rows = append(rows, PortRow{HostIP: b.HostIP, HostPort: b.HostPort, ContainerPort: b.ContainerPort, Proto: b.Proto,
				Container: c.Name, ContainerID: c.ID, State: c.State, Stack: c.Stack, Status: "ok"})
		}
	}
	byPort := map[string][]int{}
	for i, r := range rows {
		k := fmt.Sprintf("%d/%s", r.HostPort, r.Proto)
		byPort[k] = append(byPort[k], i)
	}
	// A container that is not running conflicts when a running container
	// holds its port (it would fail to start). Containers that are not
	// running and claim the same port only share it (a warning): that is
	// normal when old and new versions of a project exist side by side.
	for _, idx := range byPort {
		if len(idx) < 2 {
			continue
		}
		for _, i := range idx {
			if rows[i].State == "running" {
				continue
			}
			for _, j := range idx {
				if i == j || rows[i].Container == rows[j].Container || !ipsOverlap(rows[i].HostIP, rows[j].HostIP) {
					continue
				}
				if rows[j].State == "running" {
					rows[i].Status, rows[i].Conflict = "conflict", "port held by running "+rows[j].Container
					break
				}
				if rows[i].Status == "ok" {
					rows[i].Status, rows[i].Conflict = "shared", "also claimed by "+rows[j].Container
				}
			}
		}
	}
	for i := range rows {
		for _, rp := range reserved {
			if rows[i].HostPort == rp.Port && rows[i].Proto == rp.Proto {
				rows[i].Status, rows[i].Conflict = "conflict", "reserved by DSM ("+rp.Name+")"
			}
		}
		if rows[i].Status == "ok" && (rows[i].HostIP == "127.0.0.1" || rows[i].HostIP == "::1") {
			rows[i].Status = "local"
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if (rows[i].Status == "conflict") != (rows[j].Status == "conflict") {
			return rows[i].Status == "conflict"
		}
		return rows[i].HostPort < rows[j].HostPort
	})
	v := PortsView{Ports: rows, Reserved: reserved}
	if ls := ports.HostListeners(a.Cfg.HostProc); ls != nil {
		v.HostChecked = true
		published := map[string]bool{}
		for _, r := range rows {
			published[fmt.Sprintf("%d/%s", r.HostPort, r.Proto)] = true
		}
		for _, l := range ls {
			if !published[fmt.Sprintf("%d/%s", l.Port, l.Proto)] {
				v.HostListeners = append(v.HostListeners, l)
			}
		}
	}
	if v.Ports == nil {
		v.Ports = []PortRow{}
	}
	return v, nil
}

func ipsOverlap(a, b string) bool {
	any := func(ip string) bool { return ip == "" || ip == "0.0.0.0" || ip == "::" }
	return a == b || any(a) || any(b)
}

type binding struct {
	HostIP        string
	HostPort      int
	ContainerPort int
	Proto         string
}

func (a *App) stoppedBindings(ctx context.Context, id string) []binding {
	var out []binding
	raw, err := a.Docker.ContainerInspect(ctx, id)
	if err != nil {
		return nil
	}
	var in struct {
		HostConfig struct {
			PortBindings map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			} `json:"PortBindings"`
		} `json:"HostConfig"`
	}
	if jsonUnmarshal(raw, &in) != nil {
		return nil
	}
	for cport, bs := range in.HostConfig.PortBindings {
		var cp int
		var proto string
		fmt.Sscanf(cport, "%d/%s", &cp, &proto)
		for _, b := range bs {
			var hp int
			if _, err := fmt.Sscanf(b.HostPort, "%d", &hp); err == nil && hp > 0 {
				out = append(out, binding{HostIP: b.HostIP, HostPort: hp, ContainerPort: cp, Proto: proto})
			}
		}
	}
	return out
}

// PortConflict explains why a port cannot be used.
type PortConflict struct {
	Kind string `json:"kind"` // container | reserved | host
	Name string `json:"name"`
}

// CheckPort reports what already uses a host port. Containers belonging to
// ignoreStack (the stack being redeployed) are not counted.
func (a *App) CheckPort(ctx context.Context, port int, proto, ignoreStack string) ([]PortConflict, error) {
	if proto == "" {
		proto = "tcp"
	}
	v, err := a.Ports(ctx)
	if err != nil {
		return nil, err
	}
	out := []PortConflict{}
	for _, r := range v.Reserved {
		if r.Port == port && r.Proto == proto {
			out = append(out, PortConflict{Kind: "reserved", Name: "DSM " + r.Name})
		}
	}
	// Only running containers hold a port; stopped ones do not block a deploy.
	for _, r := range v.Ports {
		if r.HostPort == port && r.Proto == proto && r.State == "running" && (ignoreStack == "" || r.Stack != ignoreStack) {
			out = append(out, PortConflict{Kind: "container", Name: r.Container})
		}
	}
	for _, l := range v.HostListeners {
		if l.Port == port && l.Proto == proto {
			out = append(out, PortConflict{Kind: "host", Name: fmt.Sprintf("a service on the NAS (%s)", l.IP)})
			break
		}
	}
	return out, nil
}

// FreePorts returns up to n free ports at or above start.
func (a *App) FreePorts(ctx context.Context, start, n int, proto string) ([]int, error) {
	if proto == "" {
		proto = "tcp"
	}
	v, err := a.Ports(ctx)
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	for _, r := range v.Reserved {
		if r.Proto == proto {
			used[r.Port] = true
		}
	}
	for _, r := range v.Ports {
		if r.Proto == proto {
			used[r.HostPort] = true
		}
	}
	for _, l := range v.HostListeners {
		if l.Proto == proto {
			used[l.Port] = true
		}
	}
	var out []int
	for p := max(start, 1024); p < 65536 && len(out) < n; p++ {
		if !used[p] {
			out = append(out, p)
		}
	}
	return out, nil
}

func readAllLimit(r io.Reader, n int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, n))
}
