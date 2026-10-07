package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRegistry implements the parts of the v2 API the client uses, behind
// bearer-token auth.
type fakeRegistry struct {
	mu        sync.Mutex
	tags      map[string]map[string]string // repo -> tag -> digest
	deleted   []string
	tokenHits int
}

func (f *fakeRegistry) handler(srvURL func() string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "admin" || p != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.tokenHits++
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"token": "tok:" + r.URL.Query().Get("scope")})
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer tok:") {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="test"`, srvURL()))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/v2/")
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case p == "_catalog":
			var repos []string
			for r := range f.tags {
				repos = append(repos, r)
			}
			json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
		case strings.HasSuffix(p, "/tags/list"):
			repo := strings.TrimSuffix(p, "/tags/list")
			var tags []string
			for t := range f.tags[repo] {
				tags = append(tags, t)
			}
			json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags})
		case strings.Contains(p, "/manifests/"):
			i := strings.Index(p, "/manifests/")
			repo, ref := p[:i], p[i+len("/manifests/"):]
			if r.Method == http.MethodDelete {
				for t, d := range f.tags[repo] {
					if d == ref {
						delete(f.tags[repo], t)
					}
				}
				f.deleted = append(f.deleted, repo+"@"+ref)
				w.WriteHeader(http.StatusAccepted)
				return
			}
			digest := ref
			if !strings.HasPrefix(ref, "sha256:") {
				var ok bool
				if digest, ok = f.tags[repo][ref]; !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}
			}
			w.Header().Set("Docker-Content-Digest", digest)
			w.Header().Set("Content-Type", MediaOCIManifest)
			json.NewEncoder(w).Encode(map[string]any{
				"mediaType": MediaOCIManifest,
				"config":    map[string]any{"digest": "sha256:cfg" + digest[7:], "size": 10},
				"layers":    []map[string]any{{"digest": "sha256:l1", "size": 100}, {"digest": "sha256:l2", "size": 200}},
			})
		case strings.Contains(p, "/blobs/"):
			json.NewEncoder(w).Encode(map[string]any{"created": "2026-09-28T10:00:00Z", "os": "linux", "architecture": "amd64"})
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
	return mux
}

func TestClientFlow(t *testing.T) {
	f := &fakeRegistry{tags: map[string]map[string]string{
		"azeno/api": {"2026.09.28": "sha256:aaa", "release-09": "sha256:aaa", "2026.08.10": "sha256:bbb"},
		"azeno/web": {"1": "sha256:ccc"},
	}}
	var srv *httptest.Server
	srv = httptest.NewServer(f.handler(func() string { return srv.URL }))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repos, err := c.Catalog(ctx)
	if err != nil || len(repos) != 2 || repos[0] != "azeno/api" {
		t.Fatalf("catalog: %v %v", repos, err)
	}
	tags, err := c.Tags(ctx, "azeno/api")
	if err != nil || len(tags) != 3 {
		t.Fatalf("tags: %v %v", tags, err)
	}
	infos := c.TagInfos(ctx, "azeno/api", tags)
	for _, ti := range infos {
		if ti.Error != "" {
			t.Fatalf("tag info %s: %s", ti.Tag, ti.Error)
		}
		if ti.Size != 310 || !ti.Created.Equal(time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)) {
			t.Errorf("tag %s: size %d created %v", ti.Tag, ti.Size, ti.Created)
		}
	}
	shared := SharedDigests(infos)
	if len(shared["2026.09.28"]) != 1 || shared["2026.09.28"][0] != "release-09" {
		t.Errorf("shared = %v", shared)
	}
	if err := c.DeleteManifest(ctx, "azeno/api", "sha256:aaa"); err != nil {
		t.Fatal(err)
	}
	tags, _ = c.Tags(ctx, "azeno/api")
	if len(tags) != 1 || tags[0] != "2026.08.10" {
		t.Errorf("after delete tags = %v", tags)
	}
	if err := c.DeleteManifest(ctx, "azeno/api", "latest"); err == nil {
		t.Error("expected refusal for non-digest delete")
	}
}

func TestBadCredentials(t *testing.T) {
	f := &fakeRegistry{tags: map[string]map[string]string{}}
	var srv *httptest.Server
	srv = httptest.NewServer(f.handler(func() string { return srv.URL }))
	defer srv.Close()
	c, _ := New(srv.URL, "admin", "wrong", false)
	if _, err := c.Catalog(context.Background()); err == nil {
		t.Fatal("expected auth error")
	}
}
