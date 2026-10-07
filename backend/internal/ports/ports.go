// Package ports reads listening sockets of the host and checks a port
// against containers, the DSM reserved list and the host.
package ports

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DefaultReserved lists ports used by DSM services ("port/proto name").
const DefaultReserved = "22/tcp SSH, 80/tcp HTTP, 443/tcp HTTPS, 139/tcp SMB, 445/tcp SMB, 873/tcp rsync, 5000/tcp DSM, 5001/tcp DSM, 6690/tcp Drive"

// Reserved is one reserved port.
type Reserved struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"`
	Name  string `json:"name"`
}

// ParseReserved parses "5000/tcp DSM, 6690 Drive" (proto defaults to tcp).
func ParseReserved(spec string) ([]Reserved, error) {
	var out []Reserved
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Fields(part)
		pp := strings.SplitN(fields[0], "/", 2)
		port, err := strconv.Atoi(pp[0])
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid reserved port %q", fields[0])
		}
		proto := "tcp"
		if len(pp) == 2 {
			proto = strings.ToLower(pp[1])
		}
		if proto != "tcp" && proto != "udp" {
			return nil, fmt.Errorf("invalid protocol in %q", fields[0])
		}
		out = append(out, Reserved{Port: port, Proto: proto, Name: strings.Join(fields[1:], " ")})
	}
	return out, nil
}

// Listener is a listening (tcp) or bound (udp) socket on the host.
type Listener struct {
	IP    string `json:"ip"`
	Port  int    `json:"port"`
	Proto string `json:"proto"`
}

// HostListeners parses <procDir>/net/{tcp,tcp6,udp,udp6}. procDir is "/proc"
// for a native package, or e.g. "/host/proc/1" when the host's /proc is
// mounted into the container (pid 1 = host init = host network namespace).
// It returns nil when the files cannot be read.
func HostListeners(procDir string) []Listener {
	if procDir == "" {
		return nil
	}
	var out []Listener
	for _, f := range []struct{ name, proto, state string }{
		{"tcp", "tcp", "0A"}, {"tcp6", "tcp", "0A"}, {"udp", "udp", "07"}, {"udp6", "udp", "07"},
	} {
		ls, err := parseProcNet(filepath.Join(procDir, "net", f.name), f.proto, f.state)
		if err == nil {
			out = append(out, ls...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Proto < out[j].Proto
	})
	return dedupe(out)
}

func dedupe(ls []Listener) []Listener {
	seen := map[string]bool{}
	var out []Listener
	for _, l := range ls {
		k := fmt.Sprintf("%s/%d/%s", l.IP, l.Port, l.Proto)
		if !seen[k] {
			seen[k] = true
			out = append(out, l)
		}
	}
	return out
}

func parseProcNet(path, proto, state string) ([]Listener, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseProcNet(bufio.NewScanner(f), proto, state), nil
}

// ParseProcNet parses the content of a /proc/net/{tcp,udp}[6] file.
func ParseProcNet(sc *bufio.Scanner, proto, state string) []Listener {
	var out []Listener
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[3] != state {
			continue
		}
		addr := strings.SplitN(fields[1], ":", 2)
		if len(addr) != 2 {
			continue
		}
		port, err := strconv.ParseUint(addr[1], 16, 16)
		if err != nil {
			continue
		}
		out = append(out, Listener{IP: decodeIP(addr[0]), Port: int(port), Proto: proto})
	}
	return out
}

func decodeIP(h string) string {
	b, err := hex.DecodeString(h)
	if err != nil {
		return h
	}
	// /proc stores each 32-bit word in host (little-endian) byte order.
	for i := 0; i+4 <= len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	return net.IP(b).String()
}
