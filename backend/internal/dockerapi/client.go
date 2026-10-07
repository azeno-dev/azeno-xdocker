// Package dockerapi is a small client for the Docker Engine HTTP API, covering
// what xDocker needs. It talks to the daemon over a unix socket (default
// /var/run/docker.sock) or tcp://host:port.
package dockerapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client talks to one Docker daemon.
type Client struct {
	host    string // unix:///path or tcp://host:port
	dial    func(ctx context.Context) (net.Conn, error)
	http    *http.Client
	mu      sync.Mutex
	version string // negotiated API version, e.g. "1.43"
}

// Error is a non-2xx answer from the daemon.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("docker: %s (HTTP %d)", e.Message, e.Status) }

// IsNotFound reports whether err is a 404 from the daemon.
func IsNotFound(err error) bool {
	var de *Error
	return errors.As(err, &de) && de.Status == http.StatusNotFound
}

// IsConflict reports whether err is a 409 from the daemon (e.g. image in use).
func IsConflict(err error) bool {
	var de *Error
	return errors.As(err, &de) && de.Status == http.StatusConflict
}

// New creates a client. host is "unix:///var/run/docker.sock" or "tcp://h:p".
func New(host string) (*Client, error) {
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	c := &Client{host: host}
	switch {
	case strings.HasPrefix(host, "unix://"):
		path := strings.TrimPrefix(host, "unix://")
		c.dial = func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}
	case strings.HasPrefix(host, "tcp://"):
		addr := strings.TrimPrefix(host, "tcp://")
		c.dial = func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		}
	default:
		return nil, fmt.Errorf("unsupported docker host %q", host)
	}
	c.http = &http.Client{Transport: &http.Transport{
		DialContext:         func(ctx context.Context, _, _ string) (net.Conn, error) { return c.dial(ctx) },
		MaxIdleConns:        16,
		IdleConnTimeout:     60 * time.Second,
		DisableCompression:  true,
		TLSHandshakeTimeout: 10 * time.Second,
	}}
	return c, nil
}

// Host returns the configured daemon address.
func (c *Client) Host() string { return c.host }

// maxAPI is the newest Engine API version this client is known to work with.
// DSM 7.2/7.3 Container Manager ships Docker 24 (API 1.43); newer engines
// (Docker 29+) refuse anything below 1.44.
const maxAPI = "1.47"

// Negotiate asks the daemon for its API version and picks the version used
// for later calls: the daemon's own, capped to maxAPI, but never below the
// daemon's minimum.
func (c *Client) Negotiate(ctx context.Context) (Version, error) {
	var v Version
	if err := c.doJSON(ctx, http.MethodGet, "/version", nil, nil, &v, false); err != nil {
		return v, err
	}
	use := v.APIVersion
	if versionLess(maxAPI, use) {
		use = maxAPI
	}
	if v.MinAPIVersion != "" && versionLess(use, v.MinAPIVersion) {
		use = v.MinAPIVersion
	}
	c.mu.Lock()
	c.version = use
	c.mu.Unlock()
	return v, nil
}

func (c *Client) path(p string) string {
	c.mu.Lock()
	v := c.version
	c.mu.Unlock()
	if v == "" {
		return p
	}
	return "/v" + v + p
}

func (c *Client) request(ctx context.Context, method, p string, q url.Values, body io.Reader, hdr http.Header, versioned bool) (*http.Response, error) {
	if versioned {
		p = c.path(p)
	}
	u := "http://docker" + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range hdr {
		req.Header[k] = vs
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker %s %s: %w", method, p, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var m struct {
			Message string `json:"message"`
		}
		msg := strings.TrimSpace(string(b))
		if json.Unmarshal(b, &m) == nil && m.Message != "" {
			msg = m.Message
		}
		return nil, &Error{Status: resp.StatusCode, Message: msg}
	}
	return resp, nil
}

func (c *Client) doJSON(ctx context.Context, method, p string, q url.Values, in, out any, versioned bool) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	resp, err := c.request(ctx, method, p, q, body, nil, versioned)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) get(ctx context.Context, p string, q url.Values, out any) error {
	return c.doJSON(ctx, http.MethodGet, p, q, nil, out, true)
}

func (c *Client) post(ctx context.Context, p string, q url.Values, in, out any) error {
	return c.doJSON(ctx, http.MethodPost, p, q, in, out, true)
}

// ---------- system ----------

// Version is the answer of GET /version.
type Version struct {
	Version       string `json:"Version"`
	APIVersion    string `json:"ApiVersion"`
	MinAPIVersion string `json:"MinAPIVersion"`
	Os            string `json:"Os"`
	Arch          string `json:"Arch"`
	KernelVersion string `json:"KernelVersion"`
}

// Ping checks the daemon.
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.request(ctx, http.MethodGet, "/_ping", nil, nil, nil, false)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Info returns GET /info as a generic map.
func (c *Client) Info(ctx context.Context) (map[string]any, error) {
	var m map[string]any
	return m, c.get(ctx, "/info", nil, &m)
}

// DiskUsage is a summary of GET /system/df.
type DiskUsage struct {
	LayersSize int64 `json:"LayersSize"`
	Images     []struct {
		Size       int64 `json:"Size"`
		SharedSize int64 `json:"SharedSize"`
		Containers int64 `json:"Containers"`
	} `json:"Images"`
	Containers []struct {
		SizeRw int64 `json:"SizeRw"`
	} `json:"Containers"`
	Volumes []struct {
		UsageData struct {
			Size int64 `json:"Size"`
		} `json:"UsageData"`
	} `json:"Volumes"`
	BuildCache []struct {
		Size int64 `json:"Size"`
	} `json:"BuildCache"`
}

// SystemDF returns disk usage.
func (c *Client) SystemDF(ctx context.Context) (DiskUsage, error) {
	var d DiskUsage
	return d, c.get(ctx, "/system/df", nil, &d)
}

// ---------- containers ----------

// Port is one port mapping of a container.
type Port struct {
	IP          string `json:"IP,omitempty"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort,omitempty"`
	Type        string `json:"Type"`
}

// Container is one entry of GET /containers/json.
type Container struct {
	ID         string            `json:"Id"`
	Names      []string          `json:"Names"`
	Image      string            `json:"Image"`
	ImageID    string            `json:"ImageID"`
	Command    string            `json:"Command"`
	Created    int64             `json:"Created"`
	State      string            `json:"State"`
	Status     string            `json:"Status"`
	Ports      []Port            `json:"Ports"`
	Labels     map[string]string `json:"Labels"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
}

// Name returns the container name without the leading slash.
func (c Container) Name() string {
	if len(c.Names) == 0 {
		return c.ID[:12]
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// Containers lists containers (all, including stopped ones).
func (c *Client) Containers(ctx context.Context) ([]Container, error) {
	var out []Container
	return out, c.get(ctx, "/containers/json", url.Values{"all": {"1"}}, &out)
}

// ContainerInspect returns the raw inspect JSON.
func (c *Client) ContainerInspect(ctx context.Context, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.get(ctx, "/containers/"+url.PathEscape(id)+"/json", nil, &raw)
}

// ContainerAction runs start, stop, restart, kill, pause or unpause.
func (c *Client) ContainerAction(ctx context.Context, id, action string) error {
	switch action {
	case "start", "stop", "restart", "kill", "pause", "unpause":
	default:
		return fmt.Errorf("unknown container action %q", action)
	}
	err := c.post(ctx, "/containers/"+url.PathEscape(id)+"/"+action, nil, nil, nil)
	var de *Error
	if errors.As(err, &de) && de.Status == http.StatusNotModified {
		return nil
	}
	return err
}

// ContainerRemove removes a container.
func (c *Client) ContainerRemove(ctx context.Context, id string, force, volumes bool) error {
	q := url.Values{"force": {boolStr(force)}, "v": {boolStr(volumes)}}
	return c.doJSON(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id), q, nil, nil, true)
}

// ContainerLogs streams logs. When tty is false the stream is demultiplexed.
func (c *Client) ContainerLogs(ctx context.Context, id string, tail string, follow, timestamps, tty bool) (io.ReadCloser, error) {
	q := url.Values{
		"stdout": {"1"}, "stderr": {"1"},
		"tail":       {tail},
		"follow":     {boolStr(follow)},
		"timestamps": {boolStr(timestamps)},
	}
	resp, err := c.request(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs", q, nil, nil, true)
	if err != nil {
		return nil, err
	}
	if tty {
		return resp.Body, nil
	}
	pr, pw := io.Pipe()
	go func() {
		defer resp.Body.Close()
		pw.CloseWithError(Demux(resp.Body, pw, pw))
	}()
	return pr, nil
}

// ContainerStats returns one stats sample as raw JSON. Without one-shot the
// daemon waits for a second sample, so precpu_stats is filled and CPU usage
// can be computed.
func (c *Client) ContainerStats(ctx context.Context, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.get(ctx, "/containers/"+url.PathEscape(id)+"/stats", url.Values{"stream": {"0"}}, &raw)
}

// CreateSpec is the subset of POST /containers/create that xDocker uses.
type CreateSpec struct {
	Image      string            `json:"Image"`
	Entrypoint []string          `json:"Entrypoint,omitempty"`
	Cmd        []string          `json:"Cmd,omitempty"`
	Env        []string          `json:"Env,omitempty"`
	Labels     map[string]string `json:"Labels,omitempty"`
	HostConfig struct {
		VolumesFrom []string `json:"VolumesFrom,omitempty"`
		Binds       []string `json:"Binds,omitempty"`
		AutoRemove  bool     `json:"AutoRemove,omitempty"`
	} `json:"HostConfig"`
}

// ContainerCreate creates a container and returns its id.
func (c *Client) ContainerCreate(ctx context.Context, name string, spec CreateSpec) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	q := url.Values{}
	if name != "" {
		q.Set("name", name)
	}
	return out.ID, c.post(ctx, "/containers/create", q, spec, &out)
}

// ContainerWait blocks until the container stops and returns its exit code.
func (c *Client) ContainerWait(ctx context.Context, id string) (int, error) {
	var out struct {
		StatusCode int `json:"StatusCode"`
	}
	return out.StatusCode, c.post(ctx, "/containers/"+url.PathEscape(id)+"/wait", url.Values{"condition": {"not-running"}}, nil, &out)
}

// ContainerConfig is the part of inspect needed to clone a container's setup.
type ContainerConfig struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Image  string            `json:"Image"`
		Cmd    []string          `json:"Cmd"`
		Env    []string          `json:"Env"`
		Labels map[string]string `json:"Labels"`
		Tty    bool              `json:"Tty"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
}

// ContainerConfigOf returns a typed subset of inspect.
func (c *Client) ContainerConfigOf(ctx context.Context, id string) (ContainerConfig, error) {
	var out ContainerConfig
	return out, c.get(ctx, "/containers/"+url.PathEscape(id)+"/json", nil, &out)
}

// ---------- exec ----------

// ExecCreate creates an exec instance and returns its id.
func (c *Client) ExecCreate(ctx context.Context, container string, cmd []string, tty bool) (string, error) {
	in := map[string]any{
		"AttachStdin": tty, "AttachStdout": true, "AttachStderr": true,
		"Tty": tty, "Cmd": cmd,
	}
	var out struct {
		ID string `json:"Id"`
	}
	err := c.post(ctx, "/containers/"+url.PathEscape(container)+"/exec", nil, in, &out)
	return out.ID, err
}

// ExecAttach starts an exec instance and returns the hijacked connection.
// For a tty exec the stream is raw; otherwise it is multiplexed (see Demux).
func (c *Client) ExecAttach(ctx context.Context, execID string, tty bool) (net.Conn, *bufio.Reader, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	body, _ := json.Marshal(map[string]any{"Detach": false, "Tty": tty})
	req, _ := http.NewRequest(http.MethodPost, "http://docker"+c.path("/exec/"+url.PathEscape(execID)+"/start"), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		conn.Close()
		return nil, nil, &Error{Status: resp.StatusCode, Message: strings.TrimSpace(string(b))}
	}
	return conn, br, nil
}

// ExecResize resizes the exec tty.
func (c *Client) ExecResize(ctx context.Context, execID string, rows, cols int) error {
	q := url.Values{"h": {fmt.Sprint(rows)}, "w": {fmt.Sprint(cols)}}
	return c.post(ctx, "/exec/"+url.PathEscape(execID)+"/resize", q, nil, nil)
}

// ExecInspect returns the exit code and whether the exec is still running.
func (c *Client) ExecInspect(ctx context.Context, execID string) (running bool, exitCode int, err error) {
	var out struct {
		Running  bool `json:"Running"`
		ExitCode int  `json:"ExitCode"`
	}
	err = c.get(ctx, "/exec/"+url.PathEscape(execID)+"/json", nil, &out)
	return out.Running, out.ExitCode, err
}

// ExecRun runs cmd in a container to completion and returns combined output
// and the exit code.
func (c *Client) ExecRun(ctx context.Context, container string, cmd []string) (string, int, error) {
	id, err := c.ExecCreate(ctx, container, cmd, false)
	if err != nil {
		return "", -1, err
	}
	conn, br, err := c.ExecAttach(ctx, id, false)
	if err != nil {
		return "", -1, err
	}
	defer conn.Close()
	var buf bytes.Buffer
	if err := Demux(br, &buf, &buf); err != nil && !errors.Is(err, io.EOF) {
		return buf.String(), -1, err
	}
	for i := 0; i < 50; i++ {
		running, code, err := c.ExecInspect(ctx, id)
		if err != nil {
			return buf.String(), -1, err
		}
		if !running {
			return buf.String(), code, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return buf.String(), -1, fmt.Errorf("exec did not finish")
}

// Demux splits Docker's multiplexed stdout/stderr stream.
func Demux(r io.Reader, stdout, stderr io.Writer) error {
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		n := int64(hdr[4])<<24 | int64(hdr[5])<<16 | int64(hdr[6])<<8 | int64(hdr[7])
		w := stdout
		if hdr[0] == 2 {
			w = stderr
		}
		if _, err := io.CopyN(w, r, n); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// ---------- images ----------

// Image is one entry of GET /images/json.
type Image struct {
	ID          string            `json:"Id"`
	ParentID    string            `json:"ParentId"`
	RepoTags    []string          `json:"RepoTags"`
	RepoDigests []string          `json:"RepoDigests"`
	Created     int64             `json:"Created"`
	Size        int64             `json:"Size"`
	Containers  int64             `json:"Containers"`
	Labels      map[string]string `json:"Labels"`
}

// Images lists all images.
func (c *Client) Images(ctx context.Context) ([]Image, error) {
	var out []Image
	return out, c.get(ctx, "/images/json", url.Values{"all": {"0"}}, &out)
}

// ImageInspect returns the raw inspect JSON.
func (c *Client) ImageInspect(ctx context.Context, ref string) (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.get(ctx, "/images/"+ref+"/json", nil, &raw)
}

// ImageRemove removes an image reference (repo:tag or id).
func (c *Client) ImageRemove(ctx context.Context, ref string, force bool) error {
	q := url.Values{"force": {boolStr(force)}, "noprune": {"0"}}
	return c.doJSON(ctx, http.MethodDelete, "/images/"+ref, q, nil, nil, true)
}

// ImageTag adds a tag to an image.
func (c *Client) ImageTag(ctx context.Context, src, repo, tag string) error {
	return c.post(ctx, "/images/"+src+"/tag", url.Values{"repo": {repo}, "tag": {tag}}, nil, nil)
}

// RegistryAuth is the credential for pull/push.
type RegistryAuth struct {
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	ServerAddress string `json:"serveraddress,omitempty"`
}

func authHeader(a *RegistryAuth) http.Header {
	h := http.Header{}
	if a == nil {
		a = &RegistryAuth{}
	}
	b, _ := json.Marshal(a)
	h.Set("X-Registry-Auth", base64.URLEncoding.EncodeToString(b))
	return h
}

// Progress is one line of a pull/push progress stream.
type Progress struct {
	Status   string `json:"status"`
	ID       string `json:"id"`
	Progress string `json:"progress"`
	Error    string `json:"error"`
}

func (c *Client) stream(ctx context.Context, p string, q url.Values, hdr http.Header, fn func(Progress)) error {
	resp, err := c.request(ctx, http.MethodPost, p, q, nil, hdr, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var pr Progress
		if err := dec.Decode(&pr); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if pr.Error != "" {
			return errors.New(pr.Error)
		}
		if fn != nil {
			fn(pr)
		}
	}
}

// ImagePull pulls ref ("nginx:1.27" or "host:5050/x/y:tag").
func (c *Client) ImagePull(ctx context.Context, ref string, auth *RegistryAuth, fn func(Progress)) error {
	name, tag := SplitRef(ref)
	return c.stream(ctx, "/images/create", url.Values{"fromImage": {name}, "tag": {tag}}, authHeader(auth), fn)
}

// ImagePush pushes ref.
func (c *Client) ImagePush(ctx context.Context, ref string, auth *RegistryAuth, fn func(Progress)) error {
	name, tag := SplitRef(ref)
	return c.stream(ctx, "/images/"+name+"/push", url.Values{"tag": {tag}}, authHeader(auth), fn)
}

// ImagesPrune removes dangling images and returns the reclaimed bytes.
func (c *Client) ImagesPrune(ctx context.Context) (int64, error) {
	var out struct {
		SpaceReclaimed int64 `json:"SpaceReclaimed"`
	}
	q := url.Values{"filters": {`{"dangling":["true"]}`}}
	return out.SpaceReclaimed, c.post(ctx, "/images/prune", q, nil, &out)
}

// SplitRef splits "repo:tag" (the repo may contain a registry host:port).
func SplitRef(ref string) (repo, tag string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	slash := strings.LastIndex(ref, "/")
	if i := strings.LastIndex(ref, ":"); i > slash {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// versionLess compares dotted API versions like "1.41" < "1.43".
func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			fmt.Sscan(pa[i], &x)
		}
		if i < len(pb) {
			fmt.Sscan(pb[i], &y)
		}
		if x != y {
			return x < y
		}
	}
	return false
}
