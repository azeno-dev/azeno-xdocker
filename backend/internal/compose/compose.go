// Package compose manages stacks: one folder per stack under the stacks
// directory (default /volume1/docker, the same layout Container Manager uses
// for its projects), driven by the `docker compose` CLI.
//
// The stacks directory must be visible at the same absolute path to xDocker
// and to the Docker daemon, because compose resolves relative bind mounts
// (./data) on the client side.
package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ComposeFiles are the file names compose looks for, in order.
var ComposeFiles = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// ValidName reports whether name is a valid compose project name.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// Manager runs compose commands.
type Manager struct {
	Dir        string // stacks directory
	DockerHost string // passed as DOCKER_HOST
	cliOnce    sync.Once
	cli        []string // e.g. ["docker","compose"] or ["docker-compose"]
	cliErr     error
	DockerBin  string
}

// CLI returns the compose command, detecting `docker compose` vs
// `docker-compose` once.
func (m *Manager) CLI() ([]string, error) {
	m.cliOnce.Do(func() {
		bin := m.DockerBin
		if bin == "" {
			bin = "docker"
		}
		if err := exec.Command(bin, "compose", "version").Run(); err == nil {
			m.cli = []string{bin, "compose"}
			return
		}
		if p, err := exec.LookPath("docker-compose"); err == nil {
			m.cli = []string{p}
			return
		}
		m.cliErr = errors.New("neither `docker compose` nor `docker-compose` is available")
	})
	return m.cli, m.cliErr
}

// Stack is a stack folder.
type Stack struct {
	Name        string `json:"name"`
	Dir         string `json:"dir"`
	ComposeFile string `json:"composeFile"`
	HasEnv      bool   `json:"hasEnv"`
	ModTime     int64  `json:"modTime"`
}

// List returns stack folders that contain a compose file.
func (m *Manager) List() ([]Stack, error) {
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		return nil, err
	}
	var out []Stack
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "@") {
			continue
		}
		if s, ok := m.stat(e.Name()); ok {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Manager) stat(name string) (Stack, bool) {
	dir := filepath.Join(m.Dir, name)
	for _, f := range ComposeFiles {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err == nil && !fi.IsDir() {
			_, envErr := os.Stat(filepath.Join(dir, ".env"))
			return Stack{Name: name, Dir: dir, ComposeFile: f, HasEnv: envErr == nil, ModTime: fi.ModTime().Unix()}, true
		}
	}
	return Stack{}, false
}

// Get returns a stack's compose and .env content.
func (m *Manager) Get(name string) (Stack, string, string, error) {
	if !ValidName(name) {
		return Stack{}, "", "", fmt.Errorf("invalid stack name %q", name)
	}
	s, ok := m.stat(name)
	if !ok {
		return Stack{}, "", "", os.ErrNotExist
	}
	y, err := os.ReadFile(filepath.Join(s.Dir, s.ComposeFile))
	if err != nil {
		return s, "", "", err
	}
	env, _ := os.ReadFile(filepath.Join(s.Dir, ".env"))
	return s, string(y), string(env), nil
}

// Save writes compose.yaml (or the existing compose file name) and .env.
func (m *Manager) Save(name, yaml, env string) (Stack, error) {
	if !ValidName(name) {
		return Stack{}, fmt.Errorf("invalid stack name %q: use lower-case letters, digits, - and _", name)
	}
	dir := filepath.Join(m.Dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Stack{}, err
	}
	file := "compose.yaml"
	if s, ok := m.stat(name); ok {
		file = s.ComposeFile
	}
	if err := writeAtomic(filepath.Join(dir, file), []byte(yaml)); err != nil {
		return Stack{}, err
	}
	envPath := filepath.Join(dir, ".env")
	if strings.TrimSpace(env) == "" {
		os.Remove(envPath)
	} else if err := writeAtomic(envPath, []byte(env)); err != nil {
		return Stack{}, err
	}
	s, _ := m.stat(name)
	return s, nil
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".xdocker-tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Remove deletes a stack folder. Call Run(name,"down") first.
func (m *Manager) Remove(name string) error {
	if !ValidName(name) {
		return fmt.Errorf("invalid stack name %q", name)
	}
	dir := filepath.Join(m.Dir, name)
	if filepath.Dir(dir) != filepath.Clean(m.Dir) {
		return fmt.Errorf("refusing to remove %s", dir)
	}
	return os.RemoveAll(dir)
}

// Actions maps UI actions to compose arguments.
var Actions = map[string][]string{
	"up":           {"up", "-d", "--remove-orphans"},
	"down":         {"down"},
	"down-volumes": {"down", "--volumes"},
	"stop":         {"stop"},
	"start":        {"start"},
	"restart":      {"restart"},
	"pull":         {"pull"},
	"validate":     {"config", "--quiet"},
}

// Run executes a compose action for a stack and returns combined output.
// For a stack folder that does not exist (a project started elsewhere),
// only actions that work by project name are possible.
func (m *Manager) Run(ctx context.Context, name, action string, extra ...string) (string, error) {
	args, ok := Actions[action]
	if !ok {
		return "", fmt.Errorf("unknown stack action %q", action)
	}
	if !ValidName(name) {
		return "", fmt.Errorf("invalid stack name %q", name)
	}
	cli, err := m.CLI()
	if err != nil {
		return "", err
	}
	full := append([]string{}, cli[1:]...)
	full = append(full, "--project-name", name)
	var dir string
	if s, ok := m.stat(name); ok {
		dir = s.Dir
		full = append(full, "--project-directory", s.Dir, "--file", filepath.Join(s.Dir, s.ComposeFile))
	}
	full = append(full, args...)
	full = append(full, extra...)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli[0], full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "COMPOSE_PROGRESS=plain", "COMPOSE_ANSI=never")
	if m.DockerHost != "" {
		cmd.Env = append(cmd.Env, "DOCKER_HOST="+m.DockerHost)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("compose %s: %w", action, err)
	}
	return out.String(), nil
}

// Logs returns the last lines of a stack's logs.
func (m *Manager) Logs(ctx context.Context, name string, tail int) (string, error) {
	cli, err := m.CLI()
	if err != nil {
		return "", err
	}
	if !ValidName(name) {
		return "", fmt.Errorf("invalid stack name %q", name)
	}
	args := append(append([]string{}, cli[1:]...), "--project-name", name)
	if s, ok := m.stat(name); ok {
		args = append(args, "--project-directory", s.Dir, "--file", filepath.Join(s.Dir, s.ComposeFile))
	}
	args = append(args, "logs", "--no-color", "--timestamps", "--tail", fmt.Sprint(tail))
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli[0], args...)
	if m.DockerHost != "" {
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+m.DockerHost)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// PublishedPorts extracts host ports from a compose file's "ports:" entries.
// It is a light-weight text scan (no YAML dependency) used for the
// pre-deploy conflict check; it understands the short syntax
// ("8080:80", "127.0.0.1:8080:80/udp", "8080-8081:80-81") and the long
// syntax's "published:" key.
func PublishedPorts(yaml string, env map[string]string) []PortSpec {
	var out []PortSpec
	inPorts := false
	portsIndent := 0
	for _, raw := range strings.Split(yaml, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.HasPrefix(trimmed, "ports:") {
			inPorts, portsIndent = true, indent
			continue
		}
		if inPorts && indent <= portsIndent {
			inPorts = false
		}
		if !inPorts {
			continue
		}
		val := ""
		proto := "tcp"
		switch {
		case strings.HasPrefix(trimmed, "- "):
			val = strings.Trim(strings.TrimSpace(trimmed[2:]), `"'`)
			if strings.HasPrefix(val, "published:") || strings.HasPrefix(val, "target:") {
				if strings.HasPrefix(val, "published:") {
					val = strings.Trim(strings.TrimSpace(strings.TrimPrefix(val, "published:")), `"'`)
					out = append(out, expandRange(expandEnv(val, env), "", proto)...)
				}
				continue
			}
		case strings.HasPrefix(trimmed, "published:"):
			val = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "published:")), `"'`)
			out = append(out, expandRange(expandEnv(val, env), "", proto)...)
			continue
		default:
			continue
		}
		val = expandEnv(val, env)
		if i := strings.LastIndex(val, "/"); i >= 0 {
			proto = strings.ToLower(val[i+1:])
			val = val[:i]
		}
		parts := splitHostParts(val)
		switch len(parts) {
		case 2:
			out = append(out, expandRange(parts[0], "", proto)...)
		case 3:
			out = append(out, expandRange(parts[1], parts[0], proto)...)
		}
	}
	return out
}

// PortSpec is a host port a stack wants to publish.
type PortSpec struct {
	IP    string `json:"ip"`
	Port  int    `json:"port"`
	Proto string `json:"proto"`
}

func splitHostParts(v string) []string {
	// IPv6 host IPs are bracketed: [::1]:8080:80
	if strings.HasPrefix(v, "[") {
		if i := strings.Index(v, "]:"); i > 0 {
			rest := strings.SplitN(v[i+2:], ":", 2)
			return append([]string{v[1:i]}, rest...)
		}
	}
	return strings.Split(v, ":")
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::?-([^}]*))?\}`)

func expandEnv(s string, env map[string]string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		sub := envRef.FindStringSubmatch(m)
		if v, ok := env[sub[1]]; ok && v != "" {
			return v
		}
		return sub[2]
	})
}

func expandRange(v, ip, proto string) []PortSpec {
	var a, b int
	if n, _ := fmt.Sscanf(v, "%d-%d", &a, &b); n == 2 && b >= a && b-a < 1000 {
		var out []PortSpec
		for p := a; p <= b; p++ {
			out = append(out, PortSpec{IP: ip, Port: p, Proto: proto})
		}
		return out
	}
	if n, _ := fmt.Sscanf(v, "%d", &a); n == 1 && a > 0 && a < 65536 {
		return []PortSpec{{IP: ip, Port: a, Proto: proto}}
	}
	return nil
}

// ParseEnv parses a .env file into a map.
func ParseEnv(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			out[strings.TrimSpace(strings.TrimPrefix(k, "export "))] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}
