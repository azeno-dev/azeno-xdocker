package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/azeno/xdocker/internal/dockerapi"
	"github.com/azeno/xdocker/internal/jobs"
	"github.com/azeno/xdocker/internal/match"
	"github.com/azeno/xdocker/internal/registry"
	"github.com/azeno/xdocker/internal/store"
)

// RegistryClient builds a client for a stored registry.
func (a *App) RegistryClient(ctx context.Context, id int64) (*registry.Client, store.Registry, error) {
	g, err := a.Store.Registry(ctx, id)
	if err != nil {
		return nil, g, err
	}
	pass, err := a.Box.Open(g.PasswordEnc)
	if err != nil {
		return nil, g, err
	}
	c, err := registry.New(g.URL, g.Username, pass, g.Insecure)
	return c, g, err
}

// RegistryInput is the editable part of a registry.
type RegistryInput struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Username  string `json:"username"`
	Password  string `json:"password"` // empty = keep
	Insecure  bool   `json:"insecure"`
	Container string `json:"container"`
}

// SaveRegistry creates (id 0) or updates a registry after testing the
// connection.
func (a *App) SaveRegistry(ctx context.Context, user string, id int64, in RegistryInput) (int64, error) {
	in.Name, in.URL = strings.TrimSpace(in.Name), strings.TrimSpace(in.URL)
	if in.Name == "" || in.URL == "" {
		return 0, errors.New("name and URL are required")
	}
	g := store.Registry{ID: id, Name: in.Name, URL: in.URL, Username: in.Username, Insecure: in.Insecure, Container: in.Container}
	if id != 0 {
		old, err := a.Store.Registry(ctx, id)
		if err != nil {
			return 0, err
		}
		g.PasswordEnc, g.Managed = old.PasswordEnc, old.Managed
	}
	if in.Password != "" {
		enc, err := a.Box.Seal(in.Password)
		if err != nil {
			return 0, err
		}
		g.PasswordEnc = enc
	}
	pass, _ := a.Box.Open(g.PasswordEnc)
	c, err := registry.New(g.URL, g.Username, pass, g.Insecure)
	if err != nil {
		return 0, err
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.Ping(pctx); err != nil {
		return 0, fmt.Errorf("cannot reach registry: %w", err)
	}
	nid, err := a.Store.SaveRegistry(ctx, g)
	a.Audit(ctx, user, "registry.save", g.Name, map[string]any{"url": g.URL}, err)
	return nid, err
}

// RepoRow is a repository with its tag count.
type RepoRow struct {
	Name string `json:"name"`
	Tags int    `json:"tags"`
}

// Repos lists repositories matching q, with tag counts.
func (a *App) Repos(ctx context.Context, id int64, q string) ([]RepoRow, error) {
	c, _, err := a.RegistryClient(ctx, id)
	if err != nil {
		return nil, err
	}
	m, err := match.Search(q)
	if err != nil {
		return nil, err
	}
	names, err := c.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	out := []RepoRow{}
	for _, n := range names {
		if !m(n) {
			continue
		}
		tags, err := c.Tags(ctx, n)
		if err != nil {
			return nil, err
		}
		if len(tags) > 0 {
			out = append(out, RepoRow{Name: n, Tags: len(tags)})
		}
	}
	return out, nil
}

// TagRow is a tag with details and the tags sharing its digest.
type TagRow struct {
	registry.TagInfo
	SharedWith []string `json:"sharedWith"`
}

// Tags lists a repository's tags with details, newest first, filtered by q
// (matched against the tag) and created-before.
func (a *App) Tags(ctx context.Context, id int64, repo, q string, before int64) ([]TagRow, error) {
	c, _, err := a.RegistryClient(ctx, id)
	if err != nil {
		return nil, err
	}
	m, err := match.Search(q)
	if err != nil {
		return nil, err
	}
	tags, err := c.Tags(ctx, repo)
	if err != nil {
		return nil, err
	}
	infos := c.TagInfos(ctx, repo, tags)
	shared := registry.SharedDigests(infos)
	out := []TagRow{}
	for _, ti := range infos {
		if !m(ti.Tag) && !m(repo+":"+ti.Tag) {
			continue
		}
		if before > 0 && !ti.Created.IsZero() && ti.Created.Unix() >= before {
			continue
		}
		sw := shared[ti.Tag]
		if sw == nil {
			sw = []string{}
		}
		out = append(out, TagRow{TagInfo: ti, SharedWith: sw})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// RepoTag names one tag.
type RepoTag struct {
	Repo string `json:"repo"`
	Tag  string `json:"tag"`
}

// PreviewTagDelete resolves tags to digests and shows which other tags
// disappear with them.
func (a *App) PreviewTagDelete(ctx context.Context, id int64, sel []RepoTag) (DeletePlan, error) {
	c, _, err := a.RegistryClient(ctx, id)
	if err != nil {
		return DeletePlan{}, err
	}
	byRepo := map[string][]string{}
	for _, rt := range sel {
		byRepo[rt.Repo] = append(byRepo[rt.Repo], rt.Tag)
	}
	var plan DeletePlan
	seenDigest := map[string]bool{}
	repos := make([]string, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	for _, repo := range repos {
		all, err := c.Tags(ctx, repo)
		if err != nil {
			return plan, err
		}
		infos := c.TagInfos(ctx, repo, all)
		byTag := map[string]registry.TagInfo{}
		byDigest := map[string][]string{}
		for _, ti := range infos {
			byTag[ti.Tag] = ti
			if ti.Digest != "" {
				byDigest[ti.Digest] = append(byDigest[ti.Digest], ti.Tag)
			}
		}
		selected := map[string]bool{}
		for _, t := range byRepo[repo] {
			selected[t] = true
		}
		for _, t := range byRepo[repo] {
			ti, ok := byTag[t]
			it := DeletePlanItem{Ref: repo + ":" + t, Digest: ti.Digest, Size: ti.Size, Action: "delete"}
			switch {
			case !ok:
				it.Action, it.Reason = "skip", "tag not found"
			case ti.Error != "" || ti.Digest == "":
				it.Action, it.Reason = "skip", "cannot resolve digest: "+ti.Error
			}
			if it.Action == "delete" {
				for _, other := range byDigest[ti.Digest] {
					if other != t && !selected[other] {
						it.AlsoRemoves = append(it.AlsoRemoves, repo+":"+other)
					}
				}
				key := repo + "@" + ti.Digest
				if seenDigest[key] {
					it.Size = 0
					it.Reason = "same digest as another selected tag"
				} else {
					seenDigest[key] = true
					plan.Bytes += ti.Size
				}
				plan.DeleteCount++
			} else {
				plan.SkipCount++
			}
			plan.Items = append(plan.Items, it)
		}
	}
	return plan, nil
}

// StartTagDelete deletes tags (by digest) as a background job, optionally
// followed by garbage collection.
func (a *App) StartTagDelete(ctx context.Context, user string, id int64, sel []RepoTag, gcAfter bool) (int64, error) {
	plan, err := a.PreviewTagDelete(ctx, id, sel)
	if err != nil {
		return 0, err
	}
	if plan.DeleteCount == 0 {
		return 0, errors.New("nothing to delete")
	}
	c, g, err := a.RegistryClient(ctx, id)
	if err != nil {
		return 0, err
	}
	return a.Jobs.Start(store.Job{Type: "registry_delete", Title: fmt.Sprintf("Delete %d tags from %s", plan.DeleteCount, g.Name), User: user, Total: len(plan.Items)},
		func(ctx context.Context, r *jobs.Run) (string, error) {
			done := map[string]error{}
			deleted, failed := 0, 0
			for _, it := range plan.Items {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				ji := store.JobItem{Target: "registry:" + g.Name, Ref: it.Ref, Digest: it.Digest, Action: it.Action, Reason: it.Reason, Bytes: it.Size}
				if it.Action == "skip" {
					ji.Result = "skipped"
					r.Item(ji)
					continue
				}
				repo, _ := dockerapi.SplitRef(it.Ref)
				key := repo + "@" + it.Digest
				err, seen := done[key]
				if !seen {
					err = c.DeleteManifest(ctx, repo, it.Digest)
					done[key] = err
				}
				if err != nil {
					ji.Result, ji.Error = "error", err.Error()
					failed++
					r.Logf("%s: %v", it.Ref, err)
				} else {
					ji.Result = "deleted"
					deleted++
					r.Logf("deleted %s (%s)", it.Ref, shortDigest(it.Digest))
				}
				r.Item(ji)
			}
			summary := fmt.Sprintf("%d tags deleted, %d skipped, %d failed", deleted, plan.SkipCount, failed)
			if gcAfter && deleted > 0 {
				freed, err := a.runGC(ctx, r, g)
				if err != nil {
					summary += "; garbage collection failed: " + err.Error()
				} else {
					summary += "; garbage collection freed " + HumanBytes(freed)
				}
			}
			a.Audit(ctx, user, "registry.bulk_delete", g.Name, map[string]any{"job": r.ID(), "deleted": deleted}, nil)
			return summary, nil
		}, nil)
}

func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// StartGC runs garbage collection as a job.
func (a *App) StartGC(ctx context.Context, user string, id int64) (int64, error) {
	g, err := a.Store.Registry(ctx, id)
	if err != nil {
		return 0, err
	}
	if g.Container == "" {
		return 0, errors.New("garbage collection needs the registry's container name (edit the registry)")
	}
	return a.Jobs.Start(store.Job{Type: "gc", Title: "Garbage collection on " + g.Name, User: user, Total: 1},
		func(ctx context.Context, r *jobs.Run) (string, error) {
			freed, err := a.runGC(ctx, r, g)
			a.Audit(ctx, user, "registry.gc", g.Name, map[string]any{"freed": freed}, err)
			if err != nil {
				return "", err
			}
			return "freed " + HumanBytes(freed), nil
		}, nil)
}

const duMarker = "XDOCKER_DU:"

// runGC stops the registry container, runs `registry garbage-collect` in a
// temporary container that shares its volumes, and starts it again.
func (a *App) runGC(ctx context.Context, r *jobs.Run, g store.Registry) (int64, error) {
	if g.Container == "" {
		return 0, errors.New("no registry container configured")
	}
	cfg, err := a.Docker.ContainerConfigOf(ctx, g.Container)
	if err != nil {
		return 0, fmt.Errorf("registry container %q: %w", g.Container, err)
	}
	configPath := "/etc/docker/registry/config.yml"
	if n := len(cfg.Config.Cmd); n > 0 {
		last := cfg.Config.Cmd[n-1]
		if strings.HasSuffix(last, ".yml") || strings.HasSuffix(last, ".yaml") {
			configPath = last
		}
	}
	s, _ := a.GetSettings(ctx)
	flag := ""
	if s.GCDeleteUntag {
		flag = "--delete-untagged "
	}
	root := `"${REGISTRY_STORAGE_FILESYSTEM_ROOTDIRECTORY:-/var/lib/registry}"`
	script := fmt.Sprintf(`echo %[1]s$(du -sk %[2]s | cut -f1); registry garbage-collect %[3]s%[4]s; rc=$?; echo %[1]s$(du -sk %[2]s | cut -f1); exit $rc`,
		duMarker, root, flag, configPath)

	wasRunning := cfg.State.Running
	if wasRunning {
		r.Logf("stopping %s (the registry is unavailable during garbage collection)", g.Container)
		if err := a.Docker.ContainerAction(ctx, cfg.ID, "stop"); err != nil {
			return 0, err
		}
		defer func() {
			sctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := a.Docker.ContainerAction(sctx, cfg.ID, "start"); err != nil {
				r.Logf("ERROR restarting %s: %v", g.Container, err)
			} else {
				r.Logf("started %s again", g.Container)
			}
		}()
	}
	spec := dockerapi.CreateSpec{
		Image:      cfg.Config.Image,
		Entrypoint: []string{"/bin/sh", "-c"},
		Cmd:        []string{script},
		Env:        cfg.Config.Env,
		Labels:     map[string]string{"xdocker.gc": "true"},
	}
	spec.HostConfig.VolumesFrom = []string{cfg.ID}
	id, err := a.Docker.ContainerCreate(ctx, "", spec)
	if err != nil {
		return 0, fmt.Errorf("create gc container: %w", err)
	}
	defer a.Docker.ContainerRemove(context.Background(), id, true, false)
	if err := a.Docker.ContainerAction(ctx, id, "start"); err != nil {
		return 0, err
	}
	code, err := a.Docker.ContainerWait(ctx, id)
	if err != nil {
		return 0, err
	}
	logs, err := a.Docker.ContainerLogs(ctx, id, "all", false, false, false)
	var out []byte
	if err == nil {
		out, _ = readAllLimit(logs, 4<<20)
		logs.Close()
	}
	var sizes []int64
	lines := 0
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), duMarker); ok {
			n, _ := strconv.ParseInt(v, 10, 64)
			sizes = append(sizes, n*1024)
			continue
		}
		// garbage-collect prints every blob it marks; keep the log readable.
		if strings.Contains(line, "eligible for deletion") || strings.Contains(line, "blobs marked") || strings.Contains(line, "Deleting") {
			if lines < 200 {
				r.Logf("%s", strings.TrimSpace(line))
			}
			lines++
		}
	}
	if code != 0 {
		return 0, fmt.Errorf("garbage-collect exited with code %d", code)
	}
	var freed int64
	if len(sizes) == 2 {
		freed = sizes[0] - sizes[1]
		r.Logf("registry storage: %s before, %s after", HumanBytes(sizes[0]), HumanBytes(sizes[1]))
	}
	r.AddBytes(freed)
	r.Item(store.JobItem{Target: "registry:" + g.Name, Ref: "garbage-collect", Action: "gc", Result: "freed", Bytes: 0})
	return freed, nil
}

// ManagedRegistryInput configures the registry xDocker deploys itself.
type ManagedRegistryInput struct {
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Image    string `json:"image"`
}

// ManagedStack is the stack name of the registry xDocker deploys.
const ManagedStack = "xdocker-registry"

// DeployManagedRegistry writes a registry stack (htpasswd auth, deletes
// enabled), deploys it and adds it to the registry list.
func (a *App) DeployManagedRegistry(ctx context.Context, user string, in ManagedRegistryInput) (int64, error) {
	if in.Port == 0 {
		in.Port = 5050
	}
	if in.Image == "" {
		in.Image = "registry:2"
	}
	if in.Username == "" || len(in.Password) < 8 {
		return 0, errors.New("username and a password of at least 8 characters are required")
	}
	conflicts, err := a.CheckPort(ctx, in.Port, "tcp", ManagedStack)
	if err != nil {
		return 0, err
	}
	if len(conflicts) > 0 {
		return 0, fmt.Errorf("port %d is in use: %s", in.Port, conflicts[0].Name)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	dir := filepath.Join(a.Cfg.StacksDir, ManagedStack)
	if err := os.MkdirAll(filepath.Join(dir, "auth"), 0o755); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, "auth", "htpasswd"), []byte(in.Username+":"+string(hash)+"\n"), 0o644); err != nil {
		return 0, err
	}
	yaml := fmt.Sprintf(`# Managed by xDocker. Deletes are enabled so tags can be removed;
# garbage collection is run by xDocker.
services:
  registry:
    image: %s
    container_name: %s
    restart: unless-stopped
    ports:
      - "%d:5000"
    environment:
      REGISTRY_STORAGE_DELETE_ENABLED: "true"
      REGISTRY_AUTH: htpasswd
      REGISTRY_AUTH_HTPASSWD_REALM: xdocker
      REGISTRY_AUTH_HTPASSWD_PATH: /auth/htpasswd
    volumes:
      - ./data:/var/lib/registry
      - ./auth:/auth:ro
    labels:
      xdocker.managed-registry: "true"
`, in.Image, ManagedStack, in.Port)
	if _, err := a.Compose.Save(ManagedStack, yaml, ""); err != nil {
		return 0, err
	}
	enc, err := a.Box.Seal(in.Password)
	if err != nil {
		return 0, err
	}
	host := a.Cfg.HostAddr
	if host == "" {
		host = "localhost"
	}
	reg := store.Registry{Name: "nas-registry", URL: fmt.Sprintf("http://%s:%d", host, in.Port), Username: in.Username,
		PasswordEnc: enc, Managed: true, Container: ManagedStack}
	if regs, err := a.Store.Registries(ctx); err == nil {
		for _, g := range regs {
			if g.Managed {
				reg.ID, reg.Name = g.ID, g.Name
			}
		}
	}
	regID, err := a.Store.SaveRegistry(ctx, reg)
	if err != nil {
		return 0, err
	}
	return a.Jobs.Start(store.Job{Type: "deploy", Title: "Deploy managed registry", User: user, Total: 1},
		func(ctx context.Context, r *jobs.Run) (string, error) {
			out, err := a.Compose.Run(ctx, ManagedStack, "up")
			for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
				r.Logf("%s", l)
			}
			a.Audit(ctx, user, "registry.deploy_managed", ManagedStack, map[string]any{"port": in.Port, "registry": regID}, err)
			if err != nil {
				return "", err
			}
			r.Item(store.JobItem{Target: "stack", Ref: ManagedStack, Action: "up", Result: "done"})
			return fmt.Sprintf("registry running on port %d", in.Port), nil
		}, nil)
}
