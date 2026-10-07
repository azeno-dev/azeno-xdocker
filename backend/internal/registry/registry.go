// Package registry is a client for the Docker Registry HTTP API v2
// (distribution/registry), covering browse, tag details and delete.
package registry

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Media types the client accepts for manifests.
const (
	MediaDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	MediaDockerList     = "application/vnd.docker.distribution.manifest.list.v2+json"
	MediaOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	MediaOCIIndex       = "application/vnd.oci.image.index.v1+json"
)

var acceptManifests = strings.Join([]string{MediaOCIIndex, MediaDockerList, MediaOCIManifest, MediaDockerManifest}, ", ")

// Client talks to one registry.
type Client struct {
	base     *url.URL
	username string
	password string
	http     *http.Client

	mu     sync.Mutex
	tokens map[string]string // scope -> bearer token
}

// Error is a non-2xx answer from the registry.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("registry: %s (HTTP %d)", e.Message, e.Status) }

// IsNotFound reports a 404.
func IsNotFound(err error) bool {
	var re *Error
	return errors.As(err, &re) && re.Status == http.StatusNotFound
}

// New creates a client. rawURL is e.g. "https://registry.nas.lan" or
// "http://localhost:5050".
func New(rawURL, username, password string, insecure bool) (*Client, error) {
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // user opted in per registry
	}
	return &Client{
		base: u, username: username, password: password,
		http:   &http.Client{Transport: tr, Timeout: 60 * time.Second},
		tokens: map[string]string{},
	}, nil
}

// Host returns host[:port] as used in image references.
func (c *Client) Host() string { return c.base.Host }

func (c *Client) do(ctx context.Context, method, p string, hdr http.Header, scope string) (*http.Response, error) {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.base.String()+p, nil)
		if err != nil {
			return nil, err
		}
		for k, vs := range hdr {
			req.Header[k] = vs
		}
		c.mu.Lock()
		tok := c.tokens[scope]
		c.mu.Unlock()
		switch {
		case tok != "":
			req.Header.Set("Authorization", "Bearer "+tok)
		case c.username != "":
			req.SetBasicAuth(c.username, c.password)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			chal := resp.Header.Get("WWW-Authenticate")
			resp.Body.Close()
			if strings.HasPrefix(strings.ToLower(chal), "bearer ") {
				if err := c.fetchToken(ctx, chal, scope); err != nil {
					return nil, err
				}
				continue
			}
			return nil, &Error{Status: http.StatusUnauthorized, Message: "authentication failed"}
		}
		if resp.StatusCode >= 400 {
			defer resp.Body.Close()
			return nil, &Error{Status: resp.StatusCode, Message: errMessage(resp.Body)}
		}
		return resp, nil
	}
	return nil, &Error{Status: http.StatusUnauthorized, Message: "authentication failed"}
}

func errMessage(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 16<<10))
	var m struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(b, &m) == nil && len(m.Errors) > 0 {
		return m.Errors[0].Code + ": " + m.Errors[0].Message
	}
	return strings.TrimSpace(string(b))
}

var chalParam = regexp.MustCompile(`(\w+)="([^"]*)"`)

func (c *Client) fetchToken(ctx context.Context, challenge, scope string) error {
	params := map[string]string{}
	for _, m := range chalParam.FindAllStringSubmatch(challenge, -1) {
		params[m[1]] = m[2]
	}
	realm := params["realm"]
	if realm == "" {
		return errors.New("registry: bearer challenge without realm")
	}
	q := url.Values{}
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	if scope != "" {
		q.Set("scope", scope)
	} else if s := params["scope"]; s != "" {
		q.Set("scope", s)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &Error{Status: resp.StatusCode, Message: "token request failed: " + errMessage(resp.Body)}
	}
	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return err
	}
	tok := t.Token
	if tok == "" {
		tok = t.AccessToken
	}
	c.mu.Lock()
	c.tokens[scope] = tok
	c.mu.Unlock()
	return nil
}

func pullScope(repo string) string   { return "repository:" + repo + ":pull" }
func deleteScope(repo string) string { return "repository:" + repo + ":pull,delete" }

// Ping checks that the registry answers /v2/.
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/v2/", nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

var linkNext = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// Catalog lists all repositories, following pagination.
func (c *Client) Catalog(ctx context.Context) ([]string, error) {
	var all []string
	p := "/v2/_catalog?n=1000"
	for p != "" {
		resp, err := c.do(ctx, http.MethodGet, p, nil, "registry:catalog:*")
		if err != nil {
			return nil, err
		}
		var out struct {
			Repositories []string `json:"repositories"`
		}
		err = json.NewDecoder(resp.Body).Decode(&out)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, out.Repositories...)
		p = ""
		if m := linkNext.FindStringSubmatch(link); m != nil {
			p = m[1]
		}
	}
	sort.Strings(all)
	return all, nil
}

// Tags lists a repository's tags.
func (c *Client) Tags(ctx context.Context, repo string) ([]string, error) {
	var all []string
	p := "/v2/" + repo + "/tags/list?n=1000"
	for p != "" {
		resp, err := c.do(ctx, http.MethodGet, p, nil, pullScope(repo))
		if err != nil {
			if IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		var out struct {
			Tags []string `json:"tags"`
		}
		err = json.NewDecoder(resp.Body).Decode(&out)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, out.Tags...)
		p = ""
		if m := linkNext.FindStringSubmatch(link); m != nil {
			p = m[1]
		}
	}
	sort.Strings(all)
	return all, nil
}

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  *struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
		Variant      string `json:"variant"`
	} `json:"platform,omitempty"`
}

type manifest struct {
	MediaType string       `json:"mediaType"`
	Config    descriptor   `json:"config"`
	Layers    []descriptor `json:"layers"`
	Manifests []descriptor `json:"manifests"`
}

func (c *Client) getManifest(ctx context.Context, repo, ref string) (manifest, string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v2/"+repo+"/manifests/"+ref, http.Header{"Accept": {acceptManifests}}, pullScope(repo))
	if err != nil {
		return manifest{}, "", err
	}
	defer resp.Body.Close()
	var m manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&m); err != nil {
		return m, "", err
	}
	if m.MediaType == "" {
		m.MediaType = resp.Header.Get("Content-Type")
	}
	return m, resp.Header.Get("Docker-Content-Digest"), nil
}

// TagInfo is what the UI shows per tag.
type TagInfo struct {
	Repo      string    `json:"repo"`
	Tag       string    `json:"tag"`
	Digest    string    `json:"digest"`
	MediaType string    `json:"mediaType"`
	Size      int64     `json:"size"`
	Created   time.Time `json:"created"`
	Platforms []string  `json:"platforms"`
	Error     string    `json:"error,omitempty"`
}

// TagInfo fetches digest, size, creation date and platforms for one tag.
func (c *Client) TagInfo(ctx context.Context, repo, tag string) (TagInfo, error) {
	ti := TagInfo{Repo: repo, Tag: tag}
	m, digest, err := c.getManifest(ctx, repo, tag)
	if err != nil {
		return ti, err
	}
	ti.Digest, ti.MediaType = digest, m.MediaType
	if len(m.Manifests) > 0 { // index / manifest list
		for _, d := range m.Manifests {
			if d.Platform != nil && d.Platform.OS != "unknown" {
				p := d.Platform.OS + "/" + d.Platform.Architecture
				if d.Platform.Variant != "" {
					p += "/" + d.Platform.Variant
				}
				ti.Platforms = append(ti.Platforms, p)
			}
		}
		child := m.Manifests[0].Digest
		for _, d := range m.Manifests {
			if d.Platform != nil && d.Platform.Architecture == "amd64" {
				child = d.Digest
				break
			}
		}
		if m, _, err = c.getManifest(ctx, repo, child); err != nil {
			return ti, err
		}
	}
	for _, l := range m.Layers {
		ti.Size += l.Size
	}
	ti.Size += m.Config.Size
	if m.Config.Digest != "" {
		resp, err := c.do(ctx, http.MethodGet, "/v2/"+repo+"/blobs/"+m.Config.Digest, nil, pullScope(repo))
		if err == nil {
			var cfg struct {
				Created      time.Time `json:"created"`
				Architecture string    `json:"architecture"`
				OS           string    `json:"os"`
			}
			if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&cfg) == nil {
				ti.Created = cfg.Created
				if len(ti.Platforms) == 0 && cfg.OS != "" {
					ti.Platforms = []string{cfg.OS + "/" + cfg.Architecture}
				}
			}
			resp.Body.Close()
		}
	}
	return ti, nil
}

// TagInfos fetches details for many tags with limited concurrency. Tags that
// fail carry an Error instead of failing the whole call.
func (c *Client) TagInfos(ctx context.Context, repo string, tags []string) []TagInfo {
	out := make([]TagInfo, len(tags))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, t := range tags {
		wg.Add(1)
		go func(i int, t string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ti, err := c.TagInfo(ctx, repo, t)
			if err != nil {
				ti = TagInfo{Repo: repo, Tag: t, Error: err.Error()}
			}
			out[i] = ti
		}(i, t)
	}
	wg.Wait()
	return out
}

// DeleteManifest deletes a manifest by digest. Every tag pointing to that
// digest disappears with it. The registry must run with
// REGISTRY_STORAGE_DELETE_ENABLED=true.
func (c *Client) DeleteManifest(ctx context.Context, repo, digest string) error {
	if !strings.HasPrefix(digest, "sha256:") {
		return fmt.Errorf("refusing to delete %q: not a digest", digest)
	}
	resp, err := c.do(ctx, http.MethodDelete, "/v2/"+repo+"/manifests/"+digest, nil, deleteScope(repo))
	if err != nil {
		var re *Error
		if errors.As(err, &re) && re.Status == http.StatusMethodNotAllowed {
			return fmt.Errorf("registry does not allow deletes (set REGISTRY_STORAGE_DELETE_ENABLED=true): %w", err)
		}
		return err
	}
	resp.Body.Close()
	return nil
}

// SharedDigests groups tags by digest and returns, per tag, the other tags
// sharing its digest.
func SharedDigests(infos []TagInfo) map[string][]string {
	byDigest := map[string][]string{}
	for _, ti := range infos {
		if ti.Digest != "" {
			byDigest[ti.Digest] = append(byDigest[ti.Digest], ti.Tag)
		}
	}
	out := map[string][]string{}
	for _, ti := range infos {
		for _, other := range byDigest[ti.Digest] {
			if other != ti.Tag {
				out[ti.Tag] = append(out[ti.Tag], other)
			}
		}
	}
	return out
}
