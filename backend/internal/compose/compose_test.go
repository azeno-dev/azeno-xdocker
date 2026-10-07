package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedPorts(t *testing.T) {
	yaml := `
services:
  web:
    image: nginx
    ports:
      - "8080:80"
      - 127.0.0.1:5432:5432
      - "514:514/udp"
      - "9000-9001:9000-9001"
      - "${API_PORT:-7000}:7000"
      - 3000
    environment:
      - X=1
  api:
    ports:
      - target: 80
        published: "8443"
        protocol: tcp
`
	got := PublishedPorts(yaml, map[string]string{"API_PORT": "7100"})
	want := []PortSpec{
		{Port: 8080, Proto: "tcp"},
		{IP: "127.0.0.1", Port: 5432, Proto: "tcp"},
		{Port: 514, Proto: "udp"},
		{Port: 9000, Proto: "tcp"},
		{Port: 9001, Proto: "tcp"},
		{Port: 7100, Proto: "tcp"},
		{Port: 8443, Proto: "tcp"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("port %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSaveListGetRemove(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{Dir: dir}
	if _, err := m.Save("Bad Name", "", ""); err == nil {
		t.Fatal("invalid name accepted")
	}
	if _, err := m.Save("web", "services: {}\n", "A=1\n"); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "@eaDir"), 0o755)
	os.MkdirAll(filepath.Join(dir, "empty"), 0o755)
	list, err := m.List()
	if err != nil || len(list) != 1 || list[0].Name != "web" || !list[0].HasEnv {
		t.Fatalf("list = %+v %v", list, err)
	}
	_, y, env, err := m.Get("web")
	if err != nil || y != "services: {}\n" || env != "A=1\n" {
		t.Fatalf("get = %q %q %v", y, env, err)
	}
	if _, err := m.Save("web", "services: {}\n", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "web", ".env")); !os.IsNotExist(err) {
		t.Fatal(".env should be removed when empty")
	}
	if err := m.Remove("web"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "web")); !os.IsNotExist(err) {
		t.Fatal("stack dir not removed")
	}
}

func TestParseEnv(t *testing.T) {
	e := ParseEnv("# c\nA=1\nexport B=\"two\"\n\nC='3'\n")
	if e["A"] != "1" || e["B"] != "two" || e["C"] != "3" {
		t.Fatalf("env = %v", e)
	}
}
