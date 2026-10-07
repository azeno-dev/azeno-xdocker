// Package api is xDocker's HTTP layer: REST endpoints under /api/v1, the
// exec terminal websocket, log streaming and the embedded web UI.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azeno/xdocker/internal/app"
	"github.com/azeno/xdocker/internal/dockerapi"
	"github.com/azeno/xdocker/internal/registry"
	"github.com/azeno/xdocker/internal/store"
)

// Server serves the API and the UI.
type Server struct {
	app     *app.App
	sched   *app.Scheduler
	ui      fs.FS
	limiter *loginLimiter
	setupMu sync.Mutex
}

// New builds the HTTP handler. ui holds the built web UI (index.html at
// its root).
func New(a *app.App, sched *app.Scheduler, ui fs.FS) http.Handler {
	s := &Server{app: a, sched: sched, ui: ui, limiter: &loginLimiter{fail: map[string][]time.Time{}}}

	api := http.NewServeMux()
	route := func(pattern string, h http.HandlerFunc) { api.HandleFunc(pattern, h) }

	route("GET /api/v1/dashboard", s.handleDashboard)

	route("GET /api/v1/containers", s.handleContainers)
	route("GET /api/v1/containers/{id}", s.handleContainerInspect)
	route("GET /api/v1/containers/{id}/stats", s.handleContainerStats)
	route("GET /api/v1/containers/{id}/logs", s.handleContainerLogs)
	route("GET /api/v1/containers/{id}/exec", admin(s.handleExec))
	route("POST /api/v1/containers/bulk", admin(s.handleContainerBulk))

	route("GET /api/v1/images", s.handleImages)
	route("GET /api/v1/images/inspect", s.handleImageInspect)
	route("POST /api/v1/images/bulk-delete/preview", s.handleImageDeletePreview)
	route("POST /api/v1/images/bulk-delete", admin(s.handleImageDelete))
	route("POST /api/v1/images/pull", admin(s.handleImagePull))
	route("POST /api/v1/images/push", admin(s.handleImagePush))
	route("POST /api/v1/images/tag", admin(s.handleImageTag))
	route("POST /api/v1/images/prune", admin(s.handleImagePrune))

	route("GET /api/v1/stacks", s.handleStacks)
	route("GET /api/v1/stacks/{name}", s.handleStackGet)
	route("PUT /api/v1/stacks/{name}", admin(s.handleStackSave))
	route("POST /api/v1/stacks/{name}/check", s.handleStackCheck)
	route("POST /api/v1/stacks/{name}/action", admin(s.handleStackAction))
	route("GET /api/v1/stacks/{name}/logs", s.handleStackLogs)
	route("DELETE /api/v1/stacks/{name}", admin(s.handleStackDelete))

	route("GET /api/v1/ports", s.handlePorts)
	route("GET /api/v1/ports/check", s.handlePortCheck)
	route("GET /api/v1/ports/free", s.handlePortsFree)

	route("GET /api/v1/registries", s.handleRegistries)
	route("POST /api/v1/registries", admin(s.handleRegistrySave))
	route("POST /api/v1/registries/managed", admin(s.handleRegistryManaged))
	route("PUT /api/v1/registries/{id}", admin(s.handleRegistrySave))
	route("DELETE /api/v1/registries/{id}", admin(s.handleRegistryDelete))
	route("GET /api/v1/registries/{id}/repos", s.handleRepos)
	route("GET /api/v1/registries/{id}/tags", s.handleTags)
	route("POST /api/v1/registries/{id}/bulk-delete/preview", s.handleTagDeletePreview)
	route("POST /api/v1/registries/{id}/bulk-delete", admin(s.handleTagDelete))
	route("POST /api/v1/registries/{id}/gc", admin(s.handleGC))

	route("GET /api/v1/policies", s.handlePolicies)
	route("POST /api/v1/policies", admin(s.handlePolicySave))
	route("POST /api/v1/policies/preview", s.handlePolicyPreview)
	route("GET /api/v1/policies/next-runs", s.handleNextRuns)
	route("GET /api/v1/policies/{id}", s.handlePolicyGet)
	route("PUT /api/v1/policies/{id}", admin(s.handlePolicySave))
	route("DELETE /api/v1/policies/{id}", admin(s.handlePolicyDelete))
	route("POST /api/v1/policies/{id}/run", admin(s.handlePolicyRun))

	route("GET /api/v1/jobs", s.handleJobs)
	route("GET /api/v1/jobs/{id}", s.handleJob)
	route("POST /api/v1/jobs/{id}/cancel", admin(s.handleJobCancel))

	route("GET /api/v1/audit", s.handleAudit)
	route("GET /api/v1/settings", s.handleSettingsGet)
	route("PUT /api/v1/settings", admin(s.handleSettingsSave))
	route("POST /api/v1/settings/test-notify", admin(s.handleTestNotify))

	route("GET /api/v1/users", admin(s.handleUsers))
	route("POST /api/v1/users", admin(s.handleUserCreate))
	route("PUT /api/v1/users/{id}", admin(s.handleUserUpdate))
	route("DELETE /api/v1/users/{id}", admin(s.handleUserDelete))
	route("PUT /api/v1/me/password", s.handleOwnPassword)

	root := http.NewServeMux()
	root.HandleFunc("GET /api/v1/auth/state", s.handleAuthState)
	root.HandleFunc("POST /api/v1/auth/setup", s.handleSetup)
	root.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	root.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	root.Handle("/api/", s.requireAuth(api))
	root.Handle("/", s.uiHandler())
	return securityHeaders(logRequests(root))
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "same-origin")
		// SAMEORIGIN (not DENY) so DSM can show xDocker in a desktop window.
		hd.Set("X-Frame-Options", "SAMEORIGIN")
		hd.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self' ws: wss:; frame-ancestors 'self'")
		h.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

// Flush and Unwrap keep streaming and websocket upgrades working.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		h.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && (sw.status >= 400 || r.Method != http.MethodGet) {
			slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
		}
	})
}

// uiHandler serves the single-page app, falling back to index.html.
func (s *Server) uiHandler() http.Handler {
	files := http.FileServerFS(s.ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := s.ui.Open(p); err == nil {
				f.Close()
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		b, err := fs.ReadFile(s.ui, "index.html")
		if err != nil {
			http.Error(w, "web UI not built", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error     string                        `json:"error"`
	Conflicts map[string][]app.PortConflict `json:"conflicts,omitempty"`
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	b := errorBody{Error: err.Error()}
	var pc *app.PortConflictError
	if errors.As(err, &pc) {
		b.Conflicts = pc.Conflicts
	}
	json.NewEncoder(w).Encode(b)
}

// fail maps known errors to HTTP status codes.
func fail(w http.ResponseWriter, err error) {
	var pc *app.PortConflictError
	switch {
	case errors.Is(err, store.ErrNotFound), dockerapi.IsNotFound(err), registry.IsNotFound(err):
		writeError(w, http.StatusNotFound, err)
	case errors.As(err, &pc), dockerapi.IsConflict(err):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusBadRequest, err)
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid JSON body: "+err.Error()))
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid id"))
		return 0, false
	}
	return id, true
}

func userName(r *http.Request) string {
	u, _ := currentUser(r)
	return u.Username
}

func qInt(r *http.Request, key string, def int64) int64 {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func qBool(r *http.Request, key string) bool {
	v := r.URL.Query().Get(key)
	return v == "1" || v == "true"
}

type jobStarted struct {
	JobID int64 `json:"jobId"`
}
