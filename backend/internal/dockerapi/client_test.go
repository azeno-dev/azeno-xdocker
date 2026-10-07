package dockerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestSplitRef(t *testing.T) {
	cases := map[string][2]string{
		"nginx":                              {"nginx", "latest"},
		"nginx:1.27":                         {"nginx", "1.27"},
		"registry.nas.lan:5050/azeno/api":    {"registry.nas.lan:5050/azeno/api", "latest"},
		"registry.nas.lan:5050/azeno/api:v1": {"registry.nas.lan:5050/azeno/api", "v1"},
		"x@sha256:abc":                       {"x", "sha256:abc"},
	}
	for in, want := range cases {
		r, tg := SplitRef(in)
		if r != want[0] || tg != want[1] {
			t.Errorf("SplitRef(%q) = %q,%q want %q,%q", in, r, tg, want[0], want[1])
		}
	}
}

func TestDemux(t *testing.T) {
	var in bytes.Buffer
	frame := func(stream byte, s string) {
		in.Write([]byte{stream, 0, 0, 0, 0, 0, 0, byte(len(s))})
		in.WriteString(s)
	}
	frame(1, "out1\n")
	frame(2, "err1\n")
	frame(1, "out2\n")
	var out, errb bytes.Buffer
	if err := Demux(&in, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if out.String() != "out1\nout2\n" || errb.String() != "err1\n" {
		t.Fatalf("out=%q err=%q", out.String(), errb.String())
	}
}

func TestNegotiateNewEngine(t *testing.T) {
	dir, err := os.MkdirTemp("", "xd")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ln, err := net.Listen("unix", filepath.Join(dir, "d.sock"))
	if err != nil {
		t.Skip(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Version{Version: "29.8.0", APIVersion: "1.56", MinAPIVersion: "1.44"})
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()
	c, _ := New("unix://" + filepath.Join(dir, "d.sock"))
	if _, err := c.Negotiate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.path("/x"); got != "/v"+maxAPI+"/x" {
		t.Fatalf("path = %s", got)
	}
}

func TestVersionLess(t *testing.T) {
	if !versionLess("1.41", "1.43") || versionLess("1.43", "1.43") || !versionLess("1.9", "1.10") {
		t.Fatal("versionLess wrong")
	}
}

// A fake daemon on a unix socket checks negotiation and error mapping.
func TestClientAgainstFakeDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("", "xd")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Version{Version: "24.0.2", APIVersion: "1.43", MinAPIVersion: "1.12"})
	})
	mux.HandleFunc("/v1.43/containers/json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Container{{ID: "abc", Names: []string{"/web"}}})
	})
	mux.HandleFunc("/v1.43/images/busy:1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"message":"image is being used"}`))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	c, _ := New("unix://" + sock)
	ctx := context.Background()
	v, err := c.Negotiate(ctx)
	if err != nil || v.Version != "24.0.2" {
		t.Fatalf("negotiate: %v %v", v, err)
	}
	cs, err := c.Containers(ctx)
	if err != nil || len(cs) != 1 || cs[0].Name() != "web" {
		t.Fatalf("containers: %v %v", cs, err)
	}
	if err := c.ImageRemove(ctx, "busy:1", false); !IsConflict(err) {
		t.Fatalf("want conflict, got %v", err)
	}
}
