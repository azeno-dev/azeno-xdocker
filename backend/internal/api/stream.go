package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// handleContainerLogs returns the last lines as text, or streams them as
// server-sent events when follow=1.
func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "500"
	}
	follow := qBool(r, "follow")
	tty := s.app.ContainerTTY(r.Context(), id)
	rc, err := s.app.Docker.ContainerLogs(r.Context(), id, tail, follow, qBool(r, "timestamps"), tty)
	if err != nil {
		fail(w, err)
		return
	}
	defer rc.Close()
	if !follow {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.Copy(w, io.LimitReader(rc, 32<<20))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // DSM's nginx reverse proxy
	flusher.Flush()

	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-r.Context().Done():
				return
			}
		}
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			io.WriteString(w, ": ping\n\n")
			flusher.Flush()
		case l, ok := <-lines:
			if !ok {
				io.WriteString(w, "event: end\ndata: \n\n")
				flusher.Flush()
				return
			}
			b, _ := json.Marshal(l)
			io.WriteString(w, "data: ")
			w.Write(b)
			io.WriteString(w, "\n\n")
			// Batch: flush only when no more lines are waiting.
			if len(lines) == 0 {
				flusher.Flush()
			}
		}
	}
}

// handleExec opens an interactive shell in a container over a websocket.
// Client → server: binary/text frames are stdin; a text frame starting with
// {"resize":...} resizes the tty. Server → client: binary frames of output.
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	shell := r.URL.Query().Get("shell")
	cmd := []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"}
	if shell == "sh" {
		cmd = []string{"/bin/sh"}
	}
	// websocket.Accept rejects cross-origin requests by default, which
	// protects this endpoint from cross-site websocket hijacking.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	execID, err := s.app.Docker.ExecCreate(ctx, id, cmd, true)
	if err != nil {
		conn.Close(websocket.StatusInternalError, truncate(err.Error()))
		return
	}
	dconn, br, err := s.app.Docker.ExecAttach(ctx, execID, true)
	if err != nil {
		conn.Close(websocket.StatusInternalError, truncate(err.Error()))
		return
	}
	defer dconn.Close()
	s.app.Audit(ctx, userName(r), "container.exec", id, nil, nil)

	go func() {
		defer cancel()
		buf := make([]byte, 32<<10)
		for {
			n, err := br.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageText && strings.HasPrefix(string(data), `{"resize"`) {
			var m struct {
				Resize struct{ Rows, Cols int } `json:"resize"`
			}
			if json.Unmarshal(data, &m) == nil && m.Resize.Rows > 0 && m.Resize.Cols > 0 {
				s.app.Docker.ExecResize(ctx, execID, m.Resize.Rows, m.Resize.Cols)
			}
			continue
		}
		if _, err := dconn.Write(data); err != nil {
			return
		}
	}
}

func truncate(s string) string {
	if len(s) > 120 {
		return s[:120]
	}
	return s
}
