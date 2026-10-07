package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/azeno/xdocker/internal/app"
	"github.com/azeno/xdocker/internal/dockerapi"
)

// fakeDocker serves the Engine API endpoints the tests touch.
type fakeDocker struct {
	mu      sync.Mutex
	images  []dockerapi.Image
	conts   []dockerapi.Container
	removed []string
}

func (f *fakeDocker) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(dockerapi.Version{Version: "24.0.2", APIVersion: "1.43"})
	})
	mux.HandleFunc("GET /v1.43/containers/json", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(f.conts)
	})
	mux.HandleFunc("GET /v1.43/containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Id":"x","Config":{"Tty":false},"HostConfig":{"PortBindings":{}},"State":{"Running":false}}`))
	})
	mux.HandleFunc("GET /v1.43/images/json", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(f.images)
	})
	mux.HandleFunc("DELETE /v1.43/images/{ref...}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		ref := r.PathValue("ref")
		f.removed = append(f.removed, ref)
		for i := range f.images {
			var keep []string
			for _, t := range f.images[i].RepoTags {
				if t != ref {
					keep = append(keep, t)
				}
			}
			f.images[i].RepoTags = keep
		}
		w.Write([]byte(`[{"Untagged":"` + ref + `"}]`))
	})
	mux.HandleFunc("GET /v1.43/system/df", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"LayersSize":1000}`))
	})
	return mux
}

type client struct {
	t    *testing.T
	base string
	http *http.Client
	csrf string
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if c.csrf != "" {
		req.Header.Set(csrfHeader, c.csrf)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func setup(t *testing.T) (*client, *fakeDocker, *app.App) {
	t.Helper()
	dir, err := os.MkdirTemp("", "xdapi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	fd := &fakeDocker{
		images: []dockerapi.Image{
			{ID: "sha256:aaaa1111aaaa1111", RepoTags: []string{"azeno/api:2026.10.05"}, Created: time.Now().Unix(), Size: 400},
			{ID: "sha256:bbbb2222bbbb2222", RepoTags: []string{"azeno/api:2026.09.15"}, Created: time.Now().Add(-48 * time.Hour).Unix(), Size: 300},
			{ID: "sha256:cccc3333cccc3333", RepoTags: []string{"azeno/api:2026.08.10", "azeno/api:old"}, Created: time.Now().Add(-96 * time.Hour).Unix(), Size: 200},
		},
		conts: []dockerapi.Container{{ID: "c1", Names: []string{"/api-prod"}, ImageID: "sha256:aaaa1111aaaa1111", State: "running"}},
	}
	dsrv := &http.Server{Handler: fd.handler()}
	go dsrv.Serve(ln)
	t.Cleanup(func() { dsrv.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, err := app.New(ctx, app.Config{DataDir: filepath.Join(dir, "data"), DockerHost: "unix://" + sock, StacksDir: filepath.Join(dir, "stacks"), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Docker.Negotiate(ctx); err != nil {
		t.Fatal(err)
	}
	sched := app.NewScheduler(a)
	go sched.Run(ctx)
	ui := fstest.MapFS{"index.html": {Data: []byte("<html>ui</html>")}}
	srv := httptest.NewServer(New(a, sched, ui))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: srv.URL, http: &http.Client{Jar: jar}}, fd, a
}

func TestAuthFlow(t *testing.T) {
	c, _, _ := setup(t)
	var st authState
	c.do("GET", "/api/v1/auth/state", nil, &st)
	if !st.SetupRequired {
		t.Fatal("setup should be required")
	}
	if code := c.do("GET", "/api/v1/images", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous images = %d", code)
	}
	if code := c.do("POST", "/api/v1/auth/setup", credentials{"admin", "short"}, nil); code != http.StatusBadRequest {
		t.Fatalf("short password = %d", code)
	}
	if code := c.do("POST", "/api/v1/auth/setup", credentials{"admin", "correct horse"}, &st); code != 200 || st.CSRF == "" {
		t.Fatalf("setup = %d %+v", code, st)
	}
	if code := c.do("POST", "/api/v1/auth/setup", credentials{"evil", "correct horse"}, nil); code != http.StatusConflict {
		t.Fatalf("second setup = %d", code)
	}
	// State-changing call without CSRF header is refused.
	c.csrf = ""
	if code := c.do("POST", "/api/v1/images/prune", nil, nil); code != http.StatusForbidden {
		t.Fatalf("no csrf = %d", code)
	}
	c.csrf = st.CSRF
	if code := c.do("GET", "/api/v1/images", nil, nil); code != 200 {
		t.Fatalf("images = %d", code)
	}
	// Viewer cannot delete.
	if code := c.do("POST", "/api/v1/users", userIn{"bob", "password1", "viewer"}, nil); code != 200 {
		t.Fatalf("create viewer = %d", code)
	}
	c.do("POST", "/api/v1/auth/logout", nil, nil)
	if code := c.do("POST", "/api/v1/auth/login", credentials{"bob", "wrong-pass"}, nil); code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", code)
	}
	c.do("POST", "/api/v1/auth/login", credentials{"bob", "password1"}, &st)
	c.csrf = st.CSRF
	if code := c.do("POST", "/api/v1/images/bulk-delete", imageDeleteIn{Refs: []string{"x"}}, nil); code != http.StatusForbidden {
		t.Fatalf("viewer delete = %d", code)
	}
	// SPA fallback serves index.html for client routes.
	resp, _ := http.Get(c.base + "/images")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "ui") {
		t.Fatalf("spa fallback = %q", b)
	}
}

func TestLoginThrottle(t *testing.T) {
	c, _, _ := setup(t)
	c.do("POST", "/api/v1/auth/setup", credentials{"admin", "correct horse"}, nil)
	c.do("POST", "/api/v1/auth/logout", nil, nil)
	for i := 0; i < 5; i++ {
		c.do("POST", "/api/v1/auth/login", credentials{"admin", "nope-nope"}, nil)
	}
	if code := c.do("POST", "/api/v1/auth/login", credentials{"admin", "correct horse"}, nil); code != http.StatusTooManyRequests {
		t.Fatalf("throttled login = %d", code)
	}
}

func TestImageBulkDelete(t *testing.T) {
	c, fd, a := setup(t)
	var st authState
	c.do("POST", "/api/v1/auth/setup", credentials{"admin", "correct horse"}, &st)
	c.csrf = st.CSRF

	var rows []app.ImageRow
	c.do("GET", "/api/v1/images?q=azeno/*", nil, &rows)
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
	if !rows[0].Running || rows[0].UsedBy[0] != "api-prod" {
		t.Fatalf("first row = %+v", rows[0])
	}

	refs := []string{"azeno/api:2026.10.05", "azeno/api:2026.09.15", "azeno/api:2026.08.10"}
	var plan app.DeletePlan
	c.do("POST", "/api/v1/images/bulk-delete/preview", imageDeleteIn{Refs: refs}, &plan)
	if plan.DeleteCount != 2 || plan.SkipCount != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	// 2026.08.10 shares its image with "old", so only the tag goes: 300 bytes, not 500.
	if plan.Bytes != 300 {
		t.Fatalf("bytes = %d", plan.Bytes)
	}
	if code := c.do("POST", "/api/v1/images/bulk-delete", imageDeleteIn{Refs: refs, Confirm: 3}, nil); code != http.StatusConflict {
		t.Fatalf("wrong confirmation = %d", code)
	}
	var js jobStarted
	if code := c.do("POST", "/api/v1/images/bulk-delete", imageDeleteIn{Refs: refs, Confirm: 2}, &js); code != 200 {
		t.Fatalf("delete = %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, _ := a.Store.Job(context.Background(), js.JobID)
		if j.Status == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job stuck: %+v", j)
		}
		time.Sleep(20 * time.Millisecond)
	}
	fd.mu.Lock()
	defer fd.mu.Unlock()
	if strings.Join(fd.removed, ",") != "azeno/api:2026.09.15,azeno/api:2026.08.10" {
		t.Fatalf("removed = %v", fd.removed)
	}
}

func TestPolicyPreviewAndSave(t *testing.T) {
	c, _, _ := setup(t)
	var st authState
	c.do("POST", "/api/v1/auth/setup", credentials{"admin", "correct horse"}, &st)
	c.csrf = st.CSRF
	p := map[string]any{"name": "monthly", "target": "local", "repoPattern": "azeno/*", "period": "day",
		"keepPerPeriod": 1, "keepMode": "newest", "keepLastN": 1, "minAgeDays": 0, "skipInUse": true,
		"cron": "0 3 * * 0", "dryRun": true, "tagExclude": "old"}
	var pv app.PolicyPreview
	if code := c.do("POST", "/api/v1/policies/preview", p, &pv); code != 200 || pv.Local == nil {
		t.Fatalf("preview = %d %+v", code, pv)
	}
	if pv.Local.KeepCount != 4 {
		// newest (in use + last 1), 09.15 (own day), 08.10 (own day), "old" (protected)
		t.Fatalf("keep = %d decisions %+v", pv.Local.KeepCount, pv.Local.Decisions)
	}
	p["cron"] = "not a cron"
	if code := c.do("POST", "/api/v1/policies", p, nil); code != http.StatusBadRequest {
		t.Fatalf("bad cron = %d", code)
	}
	p["cron"] = "0 3 * * 0"
	p["enabled"] = true
	var saved map[string]int64
	if code := c.do("POST", "/api/v1/policies", p, &saved); code != 200 || saved["id"] == 0 {
		t.Fatalf("save = %d", code)
	}
	var list []policyOut
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.do("GET", "/api/v1/policies", nil, &list)
		if len(list) == 1 && list[0].NextRun > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(list) != 1 || list[0].NextRun == 0 {
		t.Fatalf("policies = %+v", list)
	}
}
