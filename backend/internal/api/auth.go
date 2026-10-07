package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/azeno/xdocker/internal/store"
)

const (
	sessionCookie = "xdocker_session"
	sessionTTL    = 12 * time.Hour
	csrfHeader    = "X-CSRF-Token"
)

type ctxKey int

const userKey ctxKey = 1

type session struct {
	user store.User
	csrf string
}

func currentUser(r *http.Request) (store.User, bool) {
	s, ok := r.Context().Value(userKey).(session)
	return s.user, ok
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// hashPassword returns a bcrypt hash.
func hashPassword(p string) (string, error) {
	if len(p) < 8 {
		return "", errors.New("password must have at least 8 characters")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(b), err
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User) (string, error) {
	tok, csrf := randomToken(32), randomToken(24)
	if err := s.app.Store.CreateSession(r.Context(), hashToken(tok), u.ID, csrf, time.Now().Add(sessionTTL)); err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true,
		Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds()),
	})
	return csrf, nil
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) loadSession(r *http.Request) (session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return session{}, false
	}
	u, csrf, err := s.app.Store.SessionUser(r.Context(), hashToken(c.Value))
	if err != nil {
		return session{}, false
	}
	return session{user: u, csrf: csrf}, true
}

// requireAuth wraps API handlers: it loads the session, rejects anonymous
// requests and enforces the CSRF header on state-changing methods.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.loadSession(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, errors.New("not signed in"))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			got := r.Header.Get(csrfHeader)
			if subtle.ConstantTimeCompare([]byte(got), []byte(sess.csrf)) != 1 {
				writeError(w, http.StatusForbidden, errors.New("missing or invalid CSRF token; reload the page"))
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, sess)))
	})
}

// admin wraps handlers that change state.
func admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := currentUser(r)
		if !ok || u.Role != "admin" {
			writeError(w, http.StatusForbidden, errors.New("this action needs the admin role"))
			return
		}
		h(w, r)
	}
}

// loginLimiter slows down password guessing: after 5 failures from one IP
// within 15 minutes, further attempts are refused for 15 minutes.
type loginLimiter struct {
	mu   sync.Mutex
	fail map[string][]time.Time
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	// Behind the DSM reverse proxy every request comes from localhost.
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
			return strings.TrimSpace(strings.Split(xf, ",")[0])
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return xr
		}
	}
	return host
}

func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-15 * time.Minute)
	var keep []time.Time
	for _, t := range l.fail[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	l.fail[ip] = keep
	return len(keep) >= 5
}

func (l *loginLimiter) failed(ip string) {
	l.mu.Lock()
	l.fail[ip] = append(l.fail[ip], time.Now())
	l.mu.Unlock()
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.fail, ip)
	l.mu.Unlock()
}

// ---------- handlers ----------

type authState struct {
	SetupRequired bool        `json:"setupRequired"`
	User          *store.User `json:"user,omitempty"`
	CSRF          string      `json:"csrf,omitempty"`
	Version       string      `json:"version"`
}

func (s *Server) handleAuthState(w http.ResponseWriter, r *http.Request) {
	n, err := s.app.Store.CountUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	st := authState{SetupRequired: n == 0, Version: s.app.Cfg.Version}
	if sess, ok := s.loadSession(r); ok {
		st.User, st.CSRF = &sess.user, sess.csrf
	}
	writeJSON(w, st)
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleSetup creates the first admin account. It only works while there
// are no users.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(w, r, &in) {
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	n, err := s.app.Store.CountUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, errors.New("setup is already done"))
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" {
		writeError(w, http.StatusBadRequest, errors.New("username is required"))
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	u := store.User{Username: in.Username, PasswordHash: hash, Role: "admin"}
	if u.ID, err = s.app.Store.CreateUser(r.Context(), u); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	csrf, err := s.startSession(w, r, u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.app.Audit(r.Context(), u.Username, "user.setup", u.Username, nil, nil)
	writeJSON(w, authState{User: &u, CSRF: csrf, Version: s.app.Cfg.Version})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.limiter.blocked(ip) {
		writeError(w, http.StatusTooManyRequests, errors.New("too many failed sign-ins; try again in 15 minutes"))
		return
	}
	var in credentials
	if !readJSON(w, r, &in) {
		return
	}
	u, err := s.app.Store.UserByName(r.Context(), strings.TrimSpace(in.Username))
	if err != nil || u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Password)) != nil {
		s.limiter.failed(ip)
		s.app.Audit(r.Context(), in.Username, "user.login", ip, nil, errors.New("wrong username or password"))
		writeError(w, http.StatusUnauthorized, errors.New("wrong username or password"))
		return
	}
	s.limiter.reset(ip)
	csrf, err := s.startSession(w, r, u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.app.Audit(r.Context(), u.Username, "user.login", ip, nil, nil)
	writeJSON(w, authState{User: &u, CSRF: csrf, Version: s.app.Cfg.Version})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.app.Store.DeleteSession(r.Context(), hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	w.WriteHeader(http.StatusNoContent)
}
