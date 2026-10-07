// Package app holds xDocker's operations. HTTP handlers (package api) and
// the scheduler call into it; it talks to Docker, registries and the store.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azeno/xdocker/internal/compose"
	"github.com/azeno/xdocker/internal/dockerapi"
	"github.com/azeno/xdocker/internal/jobs"
	"github.com/azeno/xdocker/internal/notify"
	"github.com/azeno/xdocker/internal/ports"
	"github.com/azeno/xdocker/internal/secret"
	"github.com/azeno/xdocker/internal/store"
)

// Config is read from the environment (see cmd/xdocker).
type Config struct {
	Listen     string // e.g. ":8790"
	DataDir    string // database, secret key
	DockerHost string // unix:///var/run/docker.sock
	DockerBin  string // docker CLI used for compose
	StacksDir  string // must be the same absolute path on the host
	HostProc   string // "/proc" (native) or "/host/proc/1" (container)
	// HostAddr is how xDocker reaches ports published on the NAS:
	// "localhost" for the native package, "host.docker.internal" in a
	// container started with extra_hosts host-gateway.
	HostAddr string
	Version  string
}

// App is the application.
type App struct {
	Cfg     Config
	Store   *store.Store
	Docker  *dockerapi.Client
	Box     *secret.Box
	Jobs    *jobs.Runner
	Compose *compose.Manager

	mu            sync.Mutex
	dockerVersion dockerapi.Version
	dockerErr     error
	policyRunning map[int64]bool
	onPolicies    func() // scheduler reload hook
}

// New wires the application.
func New(ctx context.Context, cfg Config) (*App, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "xdocker.db"))
	if err != nil {
		return nil, err
	}
	box, err := secret.Load(filepath.Join(cfg.DataDir, "secret.key"))
	if err != nil {
		st.Close()
		return nil, err
	}
	dc, err := dockerapi.New(cfg.DockerHost)
	if err != nil {
		st.Close()
		return nil, err
	}
	a := &App{
		Cfg: cfg, Store: st, Docker: dc, Box: box,
		Jobs:          jobs.New(ctx, st, 2),
		Compose:       &compose.Manager{Dir: cfg.StacksDir, DockerHost: cfg.DockerHost, DockerBin: cfg.DockerBin},
		policyRunning: map[int64]bool{},
	}
	go a.connectDocker(ctx)
	return a, nil
}

// connectDocker negotiates the API version, retrying while the daemon is
// not reachable (e.g. Container Manager still starting after boot).
func (a *App) connectDocker(ctx context.Context) {
	delay := time.Second
	for {
		v, err := a.Docker.Negotiate(ctx)
		a.mu.Lock()
		a.dockerVersion, a.dockerErr = v, err
		a.mu.Unlock()
		if err == nil {
			slog.Info("connected to docker", "version", v.Version, "api", v.APIVersion)
			return
		}
		slog.Warn("docker not reachable, retrying", "err", err, "in", delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
		}
	}
}

// DockerStatus returns the daemon version or the last connection error.
func (a *App) DockerStatus() (dockerapi.Version, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.dockerVersion, a.dockerErr
}

// Close releases resources.
func (a *App) Close() {
	a.Jobs.Wait()
	a.Store.Close()
}

// OnPoliciesChanged registers the scheduler reload hook.
func (a *App) OnPoliciesChanged(fn func()) { a.onPolicies = fn }

func (a *App) policiesChanged() {
	if a.onPolicies != nil {
		a.onPolicies()
	}
}

// Audit records an action.
func (a *App) Audit(ctx context.Context, user, action, object string, details any, result error) {
	d := ""
	switch v := details.(type) {
	case nil:
	case string:
		d = v
	default:
		b, _ := json.Marshal(v)
		d = string(b)
	}
	res := "ok"
	if result != nil {
		res = "error: " + result.Error()
	}
	if err := a.Store.Audit(context.WithoutCancel(ctx), store.AuditEntry{User: user, Action: action, Object: object, Details: d, Result: res}); err != nil {
		slog.Error("audit", "err", err)
	}
}

// ---------- settings ----------

// Settings is the typed view of the settings table.
type Settings struct {
	ReservedPorts  string `json:"reservedPorts"`
	Timezone       string `json:"timezone"`
	SMTPHost       string `json:"smtpHost"`
	SMTPPort       int    `json:"smtpPort"`
	SMTPUser       string `json:"smtpUser"`
	SMTPPassword   string `json:"smtpPassword,omitempty"` // write-only
	HasSMTPPass    bool   `json:"hasSmtpPassword"`
	SMTPFrom       string `json:"smtpFrom"`
	SMTPTo         string `json:"smtpTo"`
	WebhookURL     string `json:"webhookUrl"`
	GCDeleteUntag  bool   `json:"gcDeleteUntagged"`
	JobHistoryDays int    `json:"jobHistoryDays"`
}

// GetSettings loads settings with defaults.
func (a *App) GetSettings(ctx context.Context) (Settings, error) {
	m, err := a.Store.Settings(ctx)
	if err != nil {
		return Settings{}, err
	}
	s := Settings{
		ReservedPorts:  ports.DefaultReserved,
		Timezone:       os.Getenv("TZ"),
		SMTPPort:       587,
		JobHistoryDays: 90,
	}
	if v, ok := m["reserved_ports"]; ok {
		s.ReservedPorts = v
	}
	if v := m["timezone"]; v != "" {
		s.Timezone = v
	}
	s.SMTPHost, s.SMTPUser, s.SMTPFrom, s.SMTPTo, s.WebhookURL = m["smtp_host"], m["smtp_user"], m["smtp_from"], m["smtp_to"], m["webhook_url"]
	if p, err := strconv.Atoi(m["smtp_port"]); err == nil {
		s.SMTPPort = p
	}
	if d, err := strconv.Atoi(m["job_history_days"]); err == nil && d > 0 {
		s.JobHistoryDays = d
	}
	s.HasSMTPPass = m["smtp_pass"] != ""
	s.GCDeleteUntag = m["gc_delete_untagged"] == "1"
	return s, nil
}

// SaveSettings validates and stores settings. An empty SMTPPassword keeps
// the stored one.
func (a *App) SaveSettings(ctx context.Context, s Settings) error {
	if _, err := ports.ParseReserved(s.ReservedPorts); err != nil {
		return err
	}
	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return fmt.Errorf("unknown time zone %q", s.Timezone)
		}
	}
	kv := map[string]string{
		"reserved_ports": s.ReservedPorts, "timezone": s.Timezone,
		"smtp_host": s.SMTPHost, "smtp_port": strconv.Itoa(s.SMTPPort), "smtp_user": s.SMTPUser,
		"smtp_from": s.SMTPFrom, "smtp_to": s.SMTPTo, "webhook_url": s.WebhookURL,
		"gc_delete_untagged": map[bool]string{true: "1", false: "0"}[s.GCDeleteUntag],
		"job_history_days":   strconv.Itoa(s.JobHistoryDays),
	}
	if s.SMTPPassword != "" {
		enc, err := a.Box.Seal(s.SMTPPassword)
		if err != nil {
			return err
		}
		kv["smtp_pass"] = enc
	}
	if err := a.Store.SetSettings(ctx, kv); err != nil {
		return err
	}
	a.policiesChanged() // the default time zone may have changed
	return nil
}

// Location returns the time zone for a policy (its own, else the settings
// default, else the process default).
func (a *App) Location(ctx context.Context, tz string) *time.Location {
	if tz == "" {
		if s, err := a.GetSettings(ctx); err == nil {
			tz = s.Timezone
		}
	}
	if tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return time.Local
}

// Notify sends a message through the channels chosen.
func (a *App) Notify(ctx context.Context, email, webhook bool, m notify.Message) []error {
	s, err := a.GetSettings(ctx)
	if err != nil {
		return []error{err}
	}
	var errs []error
	if email {
		raw, _ := a.Store.Settings(ctx)
		pass, _ := a.Box.Open(raw["smtp_pass"])
		var to []string
		for _, t := range strings.Split(s.SMTPTo, ",") {
			if t = strings.TrimSpace(t); t != "" {
				to = append(to, t)
			}
		}
		if err := notify.Email(ctx, notify.SMTP{Host: s.SMTPHost, Port: s.SMTPPort, Username: s.SMTPUser, Password: pass, From: s.SMTPFrom, To: to}, m); err != nil {
			errs = append(errs, fmt.Errorf("email: %w", err))
		}
	}
	if webhook {
		if err := notify.Webhook(ctx, s.WebhookURL, m); err != nil {
			errs = append(errs, fmt.Errorf("webhook: %w", err))
		}
	}
	for _, e := range errs {
		slog.Warn("notification failed", "err", e)
	}
	return errs
}

// HumanBytes formats a byte count.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
