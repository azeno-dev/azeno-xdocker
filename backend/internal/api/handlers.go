package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/azeno/xdocker/internal/app"
	"github.com/azeno/xdocker/internal/compose"
	"github.com/azeno/xdocker/internal/notify"
	"github.com/azeno/xdocker/internal/store"
)

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.app.GetDashboard(r.Context()))
}

// ---------- containers ----------

func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	cs, err := s.app.Containers(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, cs)
}

func (s *Server) handleContainerInspect(w http.ResponseWriter, r *http.Request) {
	raw, err := s.app.Docker.ContainerInspect(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

func (s *Server) handleContainerStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.app.ContainerStats(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, st)
}

func (s *Server) handleContainerBulk(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs           []string `json:"ids"`
		Action        string   `json:"action"`
		RemoveVolumes bool     `json:"removeVolumes"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	switch in.Action {
	case "start", "stop", "restart", "kill", "pause", "unpause", "remove":
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unknown action %q", in.Action))
		return
	}
	res, err := s.app.ContainerBulk(r.Context(), userName(r), in.IDs, in.Action, in.RemoveVolumes)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, res)
}

// ---------- images ----------

func (s *Server) handleImages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, err := s.app.Images(r.Context(), app.ImageFilter{
		Q: q.Get("q"), Unused: qBool(r, "unused"), Dangling: qBool(r, "dangling"),
		Before: qInt(r, "before", 0), After: qInt(r, "after", 0), MinSize: qInt(r, "minSize", 0),
	})
	if err != nil {
		fail(w, err)
		return
	}
	if rows == nil {
		rows = []app.ImageRow{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleImageInspect(w http.ResponseWriter, r *http.Request) {
	raw, err := s.app.Docker.ImageInspect(r.Context(), r.URL.Query().Get("ref"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

type imageDeleteIn struct {
	Refs    []string `json:"refs"`
	Force   bool     `json:"force"`
	Confirm int      `json:"confirm"`
}

func (s *Server) handleImageDeletePreview(w http.ResponseWriter, r *http.Request) {
	var in imageDeleteIn
	if !readJSON(w, r, &in) {
		return
	}
	plan, err := s.app.PreviewImageDelete(r.Context(), in.Refs, in.Force)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, plan)
}

// handleImageDelete re-checks the typed confirmation on the server: the
// count typed by the user must match what would be deleted now.
func (s *Server) handleImageDelete(w http.ResponseWriter, r *http.Request) {
	var in imageDeleteIn
	if !readJSON(w, r, &in) {
		return
	}
	plan, err := s.app.PreviewImageDelete(r.Context(), in.Refs, in.Force)
	if err != nil {
		fail(w, err)
		return
	}
	if in.Confirm != plan.DeleteCount {
		writeError(w, http.StatusConflict, fmt.Errorf("confirmation does not match: %d images would be deleted now", plan.DeleteCount))
		return
	}
	id, err := s.app.StartImageDelete(r.Context(), userName(r), in.Refs, in.Force)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, jobStarted{id})
}

func (s *Server) handleImagePull(w http.ResponseWriter, r *http.Request) {
	var in struct{ Ref string }
	if !readJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Ref) == "" {
		writeError(w, http.StatusBadRequest, errors.New("image reference is required"))
		return
	}
	id, err := s.app.PullImage(r.Context(), userName(r), strings.TrimSpace(in.Ref))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, jobStarted{id})
}

func (s *Server) handleImagePush(w http.ResponseWriter, r *http.Request) {
	var in struct{ Ref string }
	if !readJSON(w, r, &in) {
		return
	}
	id, err := s.app.PushImage(r.Context(), userName(r), strings.TrimSpace(in.Ref))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, jobStarted{id})
}

func (s *Server) handleImageTag(w http.ResponseWriter, r *http.Request) {
	var in struct{ Source, Target string }
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.app.TagImage(r.Context(), userName(r), in.Source, strings.TrimSpace(in.Target)); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleImagePrune(w http.ResponseWriter, r *http.Request) {
	n, err := s.app.PruneImages(r.Context(), userName(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]int64{"reclaimed": n})
}

// ---------- stacks ----------

func (s *Server) handleStacks(w http.ResponseWriter, r *http.Request) {
	ss, err := s.app.Stacks(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"stacks": ss, "dir": s.app.Cfg.StacksDir})
}

func (s *Server) handleStackGet(w http.ResponseWriter, r *http.Request) {
	st, y, env, err := s.app.Compose.Get(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("stack %s has no compose file in %s", r.PathValue("name"), s.app.Cfg.StacksDir))
		return
	}
	writeJSON(w, map[string]any{"stack": st, "yaml": y, "env": env})
}

type stackSaveIn struct {
	YAML   string `json:"yaml"`
	Env    string `json:"env"`
	Deploy bool   `json:"deploy"`
	Force  bool   `json:"force"`
}

func (s *Server) handleStackSave(w http.ResponseWriter, r *http.Request) {
	var in stackSaveIn
	if !readJSON(w, r, &in) {
		return
	}
	name := r.PathValue("name")
	if strings.TrimSpace(in.YAML) == "" {
		writeError(w, http.StatusBadRequest, errors.New("compose file is empty"))
		return
	}
	// Keep the previous content so an invalid file can be rolled back.
	_, oldY, oldEnv, getErr := s.app.Compose.Get(name)
	if _, err := s.app.Compose.Save(name, in.YAML, in.Env); err != nil {
		fail(w, err)
		return
	}
	if out, err := s.app.Compose.Run(r.Context(), name, "validate"); err != nil {
		if getErr == nil {
			s.app.Compose.Save(name, oldY, oldEnv)
		} else {
			s.app.Compose.Remove(name)
		}
		writeError(w, http.StatusBadRequest, fmt.Errorf("compose file is not valid:\n%s", strings.TrimSpace(out)))
		return
	}
	s.app.Audit(r.Context(), userName(r), "stack.save", name, nil, nil)
	if !in.Deploy {
		writeJSON(w, map[string]any{"saved": true})
		return
	}
	id, err := s.app.StartStackAction(r.Context(), userName(r), name, "up", in.Force)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"saved": true, "jobId": id})
}

func (s *Server) handleStackCheck(w http.ResponseWriter, r *http.Request) {
	var in stackSaveIn
	if !readJSON(w, r, &in) {
		return
	}
	c, err := s.app.StackPortConflicts(r.Context(), r.PathValue("name"), in.YAML, in.Env)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"conflicts": c, "ports": compose.PublishedPorts(in.YAML, compose.ParseEnv(in.Env))})
}

func (s *Server) handleStackAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
		Force  bool   `json:"force"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	id, err := s.app.StartStackAction(r.Context(), userName(r), r.PathValue("name"), in.Action, in.Force)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, jobStarted{id})
}

func (s *Server) handleStackLogs(w http.ResponseWriter, r *http.Request) {
	out, err := s.app.Compose.Logs(r.Context(), r.PathValue("name"), int(qInt(r, "tail", 300)))
	if err != nil && out == "" {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(out))
}

func (s *Server) handleStackDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteStack(r.Context(), userName(r), r.PathValue("name"), qBool(r, "removeFiles")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- ports ----------

func (s *Server) handlePorts(w http.ResponseWriter, r *http.Request) {
	v, err := s.app.Ports(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, v)
}

func (s *Server) handlePortCheck(w http.ResponseWriter, r *http.Request) {
	port := int(qInt(r, "port", 0))
	if port < 1 || port > 65535 {
		writeError(w, http.StatusBadRequest, errors.New("port must be 1-65535"))
		return
	}
	proto := r.URL.Query().Get("proto")
	cs, err := s.app.CheckPort(r.Context(), port, proto, "")
	if err != nil {
		fail(w, err)
		return
	}
	free, _ := s.app.FreePorts(r.Context(), port, 3, proto)
	writeJSON(w, map[string]any{"port": port, "conflicts": cs, "free": len(cs) == 0, "suggestions": free})
}

func (s *Server) handlePortsFree(w http.ResponseWriter, r *http.Request) {
	free, err := s.app.FreePorts(r.Context(), int(qInt(r, "start", 8000)), int(qInt(r, "n", 5)), r.URL.Query().Get("proto"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, free)
}

// ---------- registries ----------

func (s *Server) handleRegistries(w http.ResponseWriter, r *http.Request) {
	rs, err := s.app.Store.Registries(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	if rs == nil {
		rs = []store.Registry{}
	}
	writeJSON(w, rs)
}

func (s *Server) handleRegistrySave(w http.ResponseWriter, r *http.Request) {
	var in app.RegistryInput
	if !readJSON(w, r, &in) {
		return
	}
	var id int64
	if r.PathValue("id") != "" {
		var ok bool
		if id, ok = pathID(w, r); !ok {
			return
		}
	}
	nid, err := s.app.SaveRegistry(r.Context(), userName(r), id, in)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]int64{"id": nid})
}

func (s *Server) handleRegistryManaged(w http.ResponseWriter, r *http.Request) {
	var in app.ManagedRegistryInput
	if !readJSON(w, r, &in) {
		return
	}
	id, err := s.app.DeployManagedRegistry(r.Context(), userName(r), in)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, jobStarted{id})
}

func (s *Server) handleRegistryDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.app.Store.DeleteRegistry(r.Context(), id)
	s.app.Audit(r.Context(), userName(r), "registry.remove", fmt.Sprint(id), nil, err)
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rs, err := s.app.Repos(r.Context(), id, r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, rs)
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	ts, err := s.app.Tags(r.Context(), id, q.Get("repo"), q.Get("q"), qInt(r, "before", 0))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, ts)
}

type tagDeleteIn struct {
	Items   []app.RepoTag `json:"items"`
	GCAfter bool          `json:"gcAfter"`
	Confirm int           `json:"confirm"`
}

func (s *Server) handleTagDeletePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in tagDeleteIn
	if !readJSON(w, r, &in) {
		return
	}
	plan, err := s.app.PreviewTagDelete(r.Context(), id, in.Items)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, plan)
}

func (s *Server) handleTagDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in tagDeleteIn
	if !readJSON(w, r, &in) {
		return
	}
	plan, err := s.app.PreviewTagDelete(r.Context(), id, in.Items)
	if err != nil {
		fail(w, err)
		return
	}
	if in.Confirm != plan.DeleteCount {
		writeError(w, http.StatusConflict, fmt.Errorf("confirmation does not match: %d tags would be deleted now", plan.DeleteCount))
		return
	}
	jid, err := s.app.StartTagDelete(r.Context(), userName(r), id, in.Items, in.GCAfter)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, jobStarted{jid})
}

func (s *Server) handleGC(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	jid, err := s.app.StartGC(r.Context(), userName(r), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, jobStarted{jid})
}

// ---------- policies ----------

type policyOut struct {
	store.Policy
	NextRun int64 `json:"nextRun"`
}

func (s *Server) handlePolicies(w http.ResponseWriter, r *http.Request) {
	ps, err := s.app.Store.Policies(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]policyOut, 0, len(ps))
	for _, p := range ps {
		po := policyOut{Policy: p}
		if t := s.sched.NextRun(p.ID); !t.IsZero() {
			po.NextRun = t.Unix()
		}
		out = append(out, po)
	}
	writeJSON(w, out)
}

func (s *Server) handlePolicyGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := s.app.Store.Policy(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	po := policyOut{Policy: p}
	if t := s.sched.NextRun(p.ID); !t.IsZero() {
		po.NextRun = t.Unix()
	}
	writeJSON(w, po)
}

func (s *Server) handlePolicySave(w http.ResponseWriter, r *http.Request) {
	var p store.Policy
	if !readJSON(w, r, &p) {
		return
	}
	p.ID = 0
	if r.PathValue("id") != "" {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		old, err := s.app.Store.Policy(r.Context(), id)
		if err != nil {
			fail(w, err)
			return
		}
		p.ID, p.LastRunAt, p.LastJobID = id, old.LastRunAt, old.LastJobID
	}
	if err := s.app.ValidatePolicy(r.Context(), &p); err != nil {
		fail(w, err)
		return
	}
	id, err := s.app.Store.SavePolicy(r.Context(), p)
	s.app.Audit(r.Context(), userName(r), "policy.save", p.Name, map[string]any{"enabled": p.Enabled, "dryRun": p.DryRun}, err)
	if err != nil {
		fail(w, err)
		return
	}
	s.sched.Reload()
	writeJSON(w, map[string]int64{"id": id})
}

func (s *Server) handlePolicyDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.app.Store.DeletePolicy(r.Context(), id)
	s.app.Audit(r.Context(), userName(r), "policy.delete", fmt.Sprint(id), nil, err)
	if err != nil {
		fail(w, err)
		return
	}
	s.sched.Reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePolicyPreview(w http.ResponseWriter, r *http.Request) {
	var p store.Policy
	if !readJSON(w, r, &p) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	pv, err := s.app.PreviewPolicy(ctx, p)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, pv)
}

func (s *Server) handlePolicyRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	jid, err := s.app.StartPolicyRun(r.Context(), userName(r), id, qBool(r, "dry"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, jobStarted{jid})
}

func (s *Server) handleNextRuns(w http.ResponseWriter, r *http.Request) {
	spec := r.URL.Query().Get("cron")
	if _, err := app.CronParser.Parse(spec); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	loc := s.app.Location(r.Context(), r.URL.Query().Get("tz"))
	var out []string
	for _, t := range app.NextRuns(spec, loc, 3) {
		out = append(out, t.Format(time.RFC3339))
	}
	writeJSON(w, map[string]any{"runs": out, "timezone": loc.String()})
}

// ---------- jobs, audit, settings ----------

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	js, err := s.app.Store.Jobs(r.Context(), int(qInt(r, "limit", 100)))
	if err != nil {
		fail(w, err)
		return
	}
	if js == nil {
		js = []store.Job{}
	}
	writeJSON(w, js)
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	j, err := s.app.Store.Job(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	items, err := s.app.Store.JobItems(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	if items == nil {
		items = []store.JobItem{}
	}
	writeJSON(w, map[string]any{"job": j, "items": items})
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !s.app.Jobs.Cancel(id) {
		writeError(w, http.StatusConflict, errors.New("job is not running"))
		return
	}
	s.app.Audit(r.Context(), userName(r), "job.cancel", fmt.Sprint(id), nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	es, err := s.app.Store.AuditLog(r.Context(), r.URL.Query().Get("q"), int(qInt(r, "limit", 300)))
	if err != nil {
		fail(w, err)
		return
	}
	if es == nil {
		es = []store.AuditEntry{}
	}
	writeJSON(w, es)
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.app.GetSettings(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"settings": st, "stacksDir": s.app.Cfg.StacksDir, "dockerHost": s.app.Cfg.DockerHost,
		"hostPortsChecked": s.app.Cfg.HostProc != "", "version": s.app.Cfg.Version})
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	var in app.Settings
	if !readJSON(w, r, &in) {
		return
	}
	err := s.app.SaveSettings(r.Context(), in)
	s.app.Audit(r.Context(), userName(r), "settings.save", "settings", nil, err)
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestNotify(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Webhook bool }
	if !readJSON(w, r, &in) {
		return
	}
	errs := s.app.Notify(r.Context(), in.Email, in.Webhook, notify.Message{Title: "Test notification", Text: "xDocker notifications work.", Status: "done"})
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	writeJSON(w, map[string]any{"errors": msgs})
}

// ---------- users ----------

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	us, err := s.app.Store.Users(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, us)
}

type userIn struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func validRole(r string) bool { return r == "admin" || r == "viewer" }

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	var in userIn
	if !readJSON(w, r, &in) {
		return
	}
	if !validRole(in.Role) || strings.TrimSpace(in.Username) == "" {
		writeError(w, http.StatusBadRequest, errors.New("username and role (admin or viewer) are required"))
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := s.app.Store.CreateUser(r.Context(), store.User{Username: strings.TrimSpace(in.Username), PasswordHash: hash, Role: in.Role})
	s.app.Audit(r.Context(), userName(r), "user.create", in.Username, map[string]string{"role": in.Role}, err)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]int64{"id": id})
}

func (s *Server) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in userIn
	if !readJSON(w, r, &in) {
		return
	}
	if !validRole(in.Role) {
		writeError(w, http.StatusBadRequest, errors.New("role must be admin or viewer"))
		return
	}
	if me, _ := currentUser(r); me.ID == id && in.Role != "admin" {
		writeError(w, http.StatusBadRequest, errors.New("you cannot remove your own admin role"))
		return
	}
	hash := ""
	if in.Password != "" {
		var err error
		if hash, err = hashPassword(in.Password); err != nil {
			fail(w, err)
			return
		}
	}
	err := s.app.Store.UpdateUser(r.Context(), id, in.Role, hash)
	s.app.Audit(r.Context(), userName(r), "user.update", fmt.Sprint(id), map[string]any{"role": in.Role, "passwordChanged": hash != ""}, err)
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if me, _ := currentUser(r); me.ID == id {
		writeError(w, http.StatusBadRequest, errors.New("you cannot delete your own account"))
		return
	}
	err := s.app.Store.DeleteUser(r.Context(), id)
	s.app.Audit(r.Context(), userName(r), "user.delete", fmt.Sprint(id), nil, err)
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleOwnPassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password string }
	if !readJSON(w, r, &in) {
		return
	}
	me, _ := currentUser(r)
	hash, err := hashPassword(in.Password)
	if err != nil {
		fail(w, err)
		return
	}
	err = s.app.Store.UpdateUser(r.Context(), me.ID, me.Role, hash)
	s.app.Audit(r.Context(), me.Username, "user.password", me.Username, nil, err)
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
