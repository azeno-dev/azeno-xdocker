// Package store keeps xDocker's state in SQLite: users, sessions,
// registries, retention policies, jobs, audit log and settings.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the database.
type Store struct{ db *sql.DB }

// Open opens (and migrates) the database at path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: one writer; keeps things simple and safe
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

var migrations = []string{
	`CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL DEFAULT 'viewer',
		oidc_sub TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	CREATE TABLE sessions (
		token_hash TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		csrf TEXT NOT NULL,
		expires_at INTEGER NOT NULL
	);
	CREATE TABLE registries (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		url TEXT NOT NULL,
		username TEXT NOT NULL DEFAULT '',
		password_enc TEXT NOT NULL DEFAULT '',
		insecure INTEGER NOT NULL DEFAULT 0,
		managed INTEGER NOT NULL DEFAULT 0,
		container TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE policies (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 0,
		target TEXT NOT NULL DEFAULT 'local',
		registry_id INTEGER REFERENCES registries(id) ON DELETE SET NULL,
		repo_pattern TEXT NOT NULL DEFAULT '',
		tag_include TEXT NOT NULL DEFAULT '',
		tag_exclude TEXT NOT NULL DEFAULT 'latest',
		period TEXT NOT NULL DEFAULT 'month',
		keep_per_period INTEGER NOT NULL DEFAULT 1,
		keep_mode TEXT NOT NULL DEFAULT 'newest',
		keep_last_n INTEGER NOT NULL DEFAULT 3,
		min_age_days INTEGER NOT NULL DEFAULT 7,
		max_age_days INTEGER NOT NULL DEFAULT 0,
		date_source TEXT NOT NULL DEFAULT 'created',
		date_regex TEXT NOT NULL DEFAULT '',
		date_layout TEXT NOT NULL DEFAULT '',
		skip_in_use INTEGER NOT NULL DEFAULT 1,
		cron TEXT NOT NULL DEFAULT '0 3 * * 0',
		timezone TEXT NOT NULL DEFAULT '',
		dry_run INTEGER NOT NULL DEFAULT 1,
		gc_after INTEGER NOT NULL DEFAULT 0,
		notify_email INTEGER NOT NULL DEFAULT 0,
		notify_webhook INTEGER NOT NULL DEFAULT 0,
		last_run_at INTEGER NOT NULL DEFAULT 0,
		last_job_id INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE jobs (
		id INTEGER PRIMARY KEY,
		type TEXT NOT NULL,
		title TEXT NOT NULL,
		policy_id INTEGER NOT NULL DEFAULT 0,
		user TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		done INTEGER NOT NULL DEFAULT 0,
		total INTEGER NOT NULL DEFAULT 0,
		bytes INTEGER NOT NULL DEFAULT 0,
		dry_run INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		started_at INTEGER NOT NULL DEFAULT 0,
		finished_at INTEGER NOT NULL DEFAULT 0,
		summary TEXT NOT NULL DEFAULT '',
		log TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE job_items (
		id INTEGER PRIMARY KEY,
		job_id INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
		target TEXT NOT NULL,
		ref TEXT NOT NULL,
		digest TEXT NOT NULL DEFAULT '',
		action TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		result TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		bytes INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX job_items_job ON job_items(job_id);
	CREATE TABLE audit_log (
		id INTEGER PRIMARY KEY,
		ts INTEGER NOT NULL,
		user TEXT NOT NULL,
		action TEXT NOT NULL,
		object TEXT NOT NULL,
		details TEXT NOT NULL DEFAULT '',
		result TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX audit_ts ON audit_log(ts);
	CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (v INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(v),0) FROM schema_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (v) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	// Jobs left running by a crash or restart can never finish.
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='failed', finished_at=?, summary='interrupted by restart' WHERE status IN ('queued','running')`, time.Now().Unix())
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---------- users & sessions ----------

// User is an account.
type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	CreatedAt    int64  `json:"createdAt"`
}

// CountUsers returns the number of accounts.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	return n, s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
}

// CreateUser inserts an account.
func (s *Store) CreateUser(ctx context.Context, u User) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO users (username, password_hash, role, created_at) VALUES (?,?,?,?)`,
		u.Username, u.PasswordHash, u.Role, time.Now().Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, fmt.Errorf("user %q already exists", u.Username)
		}
		return 0, err
	}
	return res.LastInsertId()
}

const userCols = `id, username, password_hash, role, created_at`

func scanUser(r interface{ Scan(...any) error }) (User, error) {
	var u User
	err := r.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// UserByName finds an account.
func (s *Store) UserByName(ctx context.Context, name string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ?`, name))
}

// Users lists accounts.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUser changes role and, when hash is non-empty, the password.
func (s *Store) UpdateUser(ctx context.Context, id int64, role, hash string) error {
	if hash != "" {
		_, err := s.db.ExecContext(ctx, `UPDATE users SET role=?, password_hash=? WHERE id=?`, role, hash, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET role=? WHERE id=?`, role, id)
	return err
}

// DeleteUser removes an account and its sessions.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	return err
}

// CreateSession stores a session.
func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, csrf string, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, csrf, expires_at) VALUES (?,?,?,?)`,
		tokenHash, userID, csrf, expires.Unix())
	return err
}

// SessionUser resolves a session to its user and CSRF token.
func (s *Store) SessionUser(ctx context.Context, tokenHash string) (User, string, error) {
	row := s.db.QueryRowContext(ctx, `SELECT u.id, u.username, u.password_hash, u.role, u.created_at, s.csrf
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, time.Now().Unix())
	var u User
	var csrf string
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt, &csrf)
	if errors.Is(err, sql.ErrNoRows) {
		return u, "", ErrNotFound
	}
	return u, csrf, err
}

// DeleteSession logs out.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ? OR expires_at < ?`, tokenHash, time.Now().Unix())
	return err
}

// ---------- registries ----------

// Registry is a configured registry. PasswordEnc is encrypted.
type Registry struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Username    string `json:"username"`
	PasswordEnc string `json:"-"`
	HasPassword bool   `json:"hasPassword"`
	Insecure    bool   `json:"insecure"`
	Managed     bool   `json:"managed"`
	Container   string `json:"container"`
}

const regCols = `id, name, url, username, password_enc, insecure, managed, container`

func scanReg(r interface{ Scan(...any) error }) (Registry, error) {
	var g Registry
	var ins, man int
	err := r.Scan(&g.ID, &g.Name, &g.URL, &g.Username, &g.PasswordEnc, &ins, &man, &g.Container)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	g.Insecure, g.Managed, g.HasPassword = ins == 1, man == 1, g.PasswordEnc != ""
	return g, err
}

// Registries lists registries.
func (s *Store) Registries(ctx context.Context) ([]Registry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+regCols+` FROM registries ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Registry
	for rows.Next() {
		g, err := scanReg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Registry returns one registry.
func (s *Store) Registry(ctx context.Context, id int64) (Registry, error) {
	return scanReg(s.db.QueryRowContext(ctx, `SELECT `+regCols+` FROM registries WHERE id = ?`, id))
}

// SaveRegistry inserts (ID 0) or updates a registry.
func (s *Store) SaveRegistry(ctx context.Context, g Registry) (int64, error) {
	if g.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO registries (name, url, username, password_enc, insecure, managed, container) VALUES (?,?,?,?,?,?,?)`,
			g.Name, g.URL, g.Username, g.PasswordEnc, b2i(g.Insecure), b2i(g.Managed), g.Container)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE registries SET name=?, url=?, username=?, password_enc=?, insecure=?, managed=?, container=? WHERE id=?`,
		g.Name, g.URL, g.Username, g.PasswordEnc, b2i(g.Insecure), b2i(g.Managed), g.Container, g.ID)
	return g.ID, err
}

// DeleteRegistry removes a registry.
func (s *Store) DeleteRegistry(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM registries WHERE id=?`, id)
	return err
}

// ---------- policies ----------

// Policy is a stored retention policy.
type Policy struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	Target        string `json:"target"` // local | registry | both
	RegistryID    int64  `json:"registryId"`
	RepoPattern   string `json:"repoPattern"`
	TagInclude    string `json:"tagInclude"`
	TagExclude    string `json:"tagExclude"`
	Period        string `json:"period"`
	KeepPerPeriod int    `json:"keepPerPeriod"`
	KeepMode      string `json:"keepMode"`
	KeepLastN     int    `json:"keepLastN"`
	MinAgeDays    int    `json:"minAgeDays"`
	MaxAgeDays    int    `json:"maxAgeDays"`
	DateSource    string `json:"dateSource"`
	DateRegex     string `json:"dateRegex"`
	DateLayout    string `json:"dateLayout"`
	SkipInUse     bool   `json:"skipInUse"`
	Cron          string `json:"cron"`
	Timezone      string `json:"timezone"`
	DryRun        bool   `json:"dryRun"`
	GCAfter       bool   `json:"gcAfter"`
	NotifyEmail   bool   `json:"notifyEmail"`
	NotifyWebhook bool   `json:"notifyWebhook"`
	LastRunAt     int64  `json:"lastRunAt"`
	LastJobID     int64  `json:"lastJobId"`
}

const polCols = `id, name, enabled, target, COALESCE(registry_id,0), repo_pattern, tag_include, tag_exclude, period,
	keep_per_period, keep_mode, keep_last_n, min_age_days, max_age_days, date_source, date_regex, date_layout,
	skip_in_use, cron, timezone, dry_run, gc_after, notify_email, notify_webhook, last_run_at, last_job_id`

func scanPolicy(r interface{ Scan(...any) error }) (Policy, error) {
	var p Policy
	var en, skip, dry, gc, ne, nw int
	err := r.Scan(&p.ID, &p.Name, &en, &p.Target, &p.RegistryID, &p.RepoPattern, &p.TagInclude, &p.TagExclude, &p.Period,
		&p.KeepPerPeriod, &p.KeepMode, &p.KeepLastN, &p.MinAgeDays, &p.MaxAgeDays, &p.DateSource, &p.DateRegex, &p.DateLayout,
		&skip, &p.Cron, &p.Timezone, &dry, &gc, &ne, &nw, &p.LastRunAt, &p.LastJobID)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.Enabled, p.SkipInUse, p.DryRun, p.GCAfter, p.NotifyEmail, p.NotifyWebhook = en == 1, skip == 1, dry == 1, gc == 1, ne == 1, nw == 1
	return p, err
}

// Policies lists policies.
func (s *Store) Policies(ctx context.Context) ([]Policy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+polCols+` FROM policies ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Policy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Policy returns one policy.
func (s *Store) Policy(ctx context.Context, id int64) (Policy, error) {
	return scanPolicy(s.db.QueryRowContext(ctx, `SELECT `+polCols+` FROM policies WHERE id=?`, id))
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// SavePolicy inserts (ID 0) or updates a policy.
func (s *Store) SavePolicy(ctx context.Context, p Policy) (int64, error) {
	args := []any{p.Name, b2i(p.Enabled), p.Target, nullID(p.RegistryID), p.RepoPattern, p.TagInclude, p.TagExclude, p.Period,
		p.KeepPerPeriod, p.KeepMode, p.KeepLastN, p.MinAgeDays, p.MaxAgeDays, p.DateSource, p.DateRegex, p.DateLayout,
		b2i(p.SkipInUse), p.Cron, p.Timezone, b2i(p.DryRun), b2i(p.GCAfter), b2i(p.NotifyEmail), b2i(p.NotifyWebhook)}
	if p.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO policies (name, enabled, target, registry_id, repo_pattern, tag_include, tag_exclude, period,
			keep_per_period, keep_mode, keep_last_n, min_age_days, max_age_days, date_source, date_regex, date_layout,
			skip_in_use, cron, timezone, dry_run, gc_after, notify_email, notify_webhook) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE policies SET name=?, enabled=?, target=?, registry_id=?, repo_pattern=?, tag_include=?, tag_exclude=?, period=?,
		keep_per_period=?, keep_mode=?, keep_last_n=?, min_age_days=?, max_age_days=?, date_source=?, date_regex=?, date_layout=?,
		skip_in_use=?, cron=?, timezone=?, dry_run=?, gc_after=?, notify_email=?, notify_webhook=? WHERE id=?`, append(args, p.ID)...)
	return p.ID, err
}

// MarkPolicyRun records the last run.
func (s *Store) MarkPolicyRun(ctx context.Context, id, jobID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE policies SET last_run_at=?, last_job_id=? WHERE id=?`, time.Now().Unix(), jobID, id)
	return err
}

// DeletePolicy removes a policy.
func (s *Store) DeletePolicy(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM policies WHERE id=?`, id)
	return err
}

// ---------- jobs ----------

// Job is a background job.
type Job struct {
	ID         int64  `json:"id"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	PolicyID   int64  `json:"policyId"`
	User       string `json:"user"`
	Status     string `json:"status"`
	Done       int    `json:"done"`
	Total      int    `json:"total"`
	Bytes      int64  `json:"bytes"`
	DryRun     bool   `json:"dryRun"`
	CreatedAt  int64  `json:"createdAt"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt"`
	Summary    string `json:"summary"`
	Log        string `json:"log,omitempty"`
}

// JobItem is one target of a job.
type JobItem struct {
	ID     int64  `json:"id"`
	JobID  int64  `json:"jobId"`
	Target string `json:"target"`
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
	Action string `json:"action"`
	Reason string `json:"reason"`
	Result string `json:"result"`
	Error  string `json:"error"`
	Bytes  int64  `json:"bytes"`
}

// CreateJob inserts a queued job.
func (s *Store) CreateJob(ctx context.Context, j Job) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO jobs (type, title, policy_id, user, status, total, dry_run, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		j.Type, j.Title, j.PolicyID, j.User, "queued", j.Total, b2i(j.DryRun), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateJob saves progress fields.
func (s *Store) UpdateJob(ctx context.Context, j Job) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?, done=?, total=?, bytes=?, started_at=?, finished_at=?, summary=?, log=? WHERE id=?`,
		j.Status, j.Done, j.Total, j.Bytes, j.StartedAt, j.FinishedAt, j.Summary, j.Log, j.ID)
	return err
}

const jobCols = `id, type, title, policy_id, user, status, done, total, bytes, dry_run, created_at, started_at, finished_at, summary`

func scanJob(r interface{ Scan(...any) error }, withLog bool) (Job, error) {
	var j Job
	var dry int
	dst := []any{&j.ID, &j.Type, &j.Title, &j.PolicyID, &j.User, &j.Status, &j.Done, &j.Total, &j.Bytes, &dry, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &j.Summary}
	if withLog {
		dst = append(dst, &j.Log)
	}
	err := r.Scan(dst...)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	j.DryRun = dry == 1
	return j, err
}

// Jobs lists recent jobs, newest first.
func (s *Store) Jobs(ctx context.Context, limit int) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobCols+` FROM jobs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Job returns one job with its log.
func (s *Store) Job(ctx context.Context, id int64) (Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobCols+`, log FROM jobs WHERE id=?`, id), true)
}

// AddJobItems inserts items for a job.
func (s *Store) AddJobItems(ctx context.Context, items []JobItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO job_items (job_id, target, ref, digest, action, reason, result, error, bytes) VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, it := range items {
		if _, err := stmt.ExecContext(ctx, it.JobID, it.Target, it.Ref, it.Digest, it.Action, it.Reason, it.Result, it.Error, it.Bytes); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// JobItems lists a job's items.
func (s *Store) JobItems(ctx context.Context, jobID int64) ([]JobItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, job_id, target, ref, digest, action, reason, result, error, bytes FROM job_items WHERE job_id=? ORDER BY id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobItem
	for rows.Next() {
		var it JobItem
		if err := rows.Scan(&it.ID, &it.JobID, &it.Target, &it.Ref, &it.Digest, &it.Action, &it.Reason, &it.Result, &it.Error, &it.Bytes); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// PruneJobs deletes finished jobs older than the cutoff.
func (s *Store) PruneJobs(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE finished_at > 0 AND finished_at < ?`, before.Unix())
	return err
}

// ---------- audit ----------

// AuditEntry is one audit log row.
type AuditEntry struct {
	ID      int64  `json:"id"`
	TS      int64  `json:"ts"`
	User    string `json:"user"`
	Action  string `json:"action"`
	Object  string `json:"object"`
	Details string `json:"details"`
	Result  string `json:"result"`
}

// Audit appends an audit entry.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_log (ts, user, action, object, details, result) VALUES (?,?,?,?,?,?)`,
		time.Now().Unix(), e.User, e.Action, e.Object, e.Details, e.Result)
	return err
}

// AuditLog lists entries, newest first, optionally filtered by text.
func (s *Store) AuditLog(ctx context.Context, q string, limit int) ([]AuditEntry, error) {
	like := "%" + q + "%"
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, user, action, object, details, result FROM audit_log
		WHERE ? = '' OR user LIKE ? OR action LIKE ? OR object LIKE ? ORDER BY id DESC LIMIT ?`, q, like, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.User, &e.Action, &e.Object, &e.Details, &e.Result); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------- settings ----------

// Settings returns all key/value settings.
func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetSettings upserts several settings.
func (s *Store) SetSettings(ctx context.Context, kv map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
