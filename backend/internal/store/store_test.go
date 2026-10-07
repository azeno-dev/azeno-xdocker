package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/azeno/xdocker/internal/secret"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUsersAndSessions(t *testing.T) {
	s, ctx := open(t), context.Background()
	id, err := s.CreateUser(ctx, User{Username: "admin", PasswordHash: "h", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, User{Username: "admin", Role: "admin"}); err == nil {
		t.Fatal("duplicate user accepted")
	}
	if err := s.CreateSession(ctx, "tok", id, "csrf1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	u, csrf, err := s.SessionUser(ctx, "tok")
	if err != nil || u.Username != "admin" || csrf != "csrf1" {
		t.Fatalf("session: %v %q %v", u, csrf, err)
	}
	s.CreateSession(ctx, "old", id, "c", time.Now().Add(-time.Minute))
	if _, _, err := s.SessionUser(ctx, "old"); err != ErrNotFound {
		t.Fatalf("expired session: %v", err)
	}
}

func TestPolicyRoundTrip(t *testing.T) {
	s, ctx := open(t), context.Background()
	p := Policy{Name: "m", Enabled: true, Target: "both", RepoPattern: "azeno/*", Period: "month", KeepPerPeriod: 1,
		KeepMode: "newest", KeepLastN: 3, MinAgeDays: 7, SkipInUse: true, Cron: "0 3 * * 0", DryRun: true, GCAfter: true}
	id, err := s.SavePolicy(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Policy(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.ID = id
	if got != p {
		t.Fatalf("got %+v\nwant %+v", got, p)
	}
	p.Enabled = false
	if _, err := s.SavePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Policy(ctx, id)
	if got.Enabled {
		t.Fatal("update not saved")
	}
}

func TestJobsAndRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, _ := s.CreateJob(ctx, Job{Type: "bulk_delete", Title: "t", Total: 2})
	s.AddJobItems(ctx, []JobItem{{JobID: id, Target: "local", Ref: "a:1", Action: "delete"}})
	j, _ := s.Job(ctx, id)
	j.Status = "running"
	s.UpdateJob(ctx, j)
	s.Close()

	s, err = Open(filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, _ = s.Job(ctx, id)
	if j.Status != "failed" {
		t.Fatalf("status after restart = %s", j.Status)
	}
	items, _ := s.JobItems(ctx, id)
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
}

func TestSettingsAndAudit(t *testing.T) {
	s, ctx := open(t), context.Background()
	s.SetSettings(ctx, map[string]string{"a": "1", "b": "2"})
	s.SetSettings(ctx, map[string]string{"a": "3"})
	m, _ := s.Settings(ctx)
	if m["a"] != "3" || m["b"] != "2" {
		t.Fatalf("settings = %v", m)
	}
	s.Audit(ctx, AuditEntry{User: "admin", Action: "image.delete", Object: "x:1", Result: "ok"})
	log, _ := s.AuditLog(ctx, "image", 10)
	if len(log) != 1 {
		t.Fatalf("audit = %v", log)
	}
}

func TestSecretBox(t *testing.T) {
	b, err := secret.Load(filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := b.Seal("pw")
	if enc == "pw" {
		t.Fatal("not encrypted")
	}
	dec, err := b.Open(enc)
	if err != nil || dec != "pw" {
		t.Fatalf("open: %q %v", dec, err)
	}
}
