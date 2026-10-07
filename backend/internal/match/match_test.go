package match

import (
	"reflect"
	"testing"
)

func TestSearch(t *testing.T) {
	cases := []struct {
		q, s string
		want bool
	}{
		{"", "anything", true},
		{"api", "azeno/api:1.0", true},
		{"API", "azeno/api:1.0", true},
		{"web", "azeno/api:1.0", false},
		{"azeno/*", "azeno/api:2026.10.05", true},
		{"azeno/*", "other/azeno/api:1", false},
		{"azeno/*:2026.09*", "azeno/api:2026.09.15", true},
		{"azeno/*:2026.09*", "azeno/api:2026.10.01", false},
		{"azeno/api:2026.0?.15", "azeno/api:2026.09.15", true},
		{"/^registry\\.local:5050/.*/", "registry.local:5050/x:1", true},
		{"/^registry\\.local:5050/.*/", "docker.io/x:1", false},
	}
	for _, c := range cases {
		m, err := Search(c.q)
		if err != nil {
			t.Fatalf("Search(%q): %v", c.q, err)
		}
		if got := m(c.s); got != c.want {
			t.Errorf("Search(%q)(%q) = %v, want %v", c.q, c.s, got, c.want)
		}
	}
}

func TestSearchInvalidRegex(t *testing.T) {
	if _, err := Search("/([/"); err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestPattern(t *testing.T) {
	cases := []struct {
		p, s string
		want bool
	}{
		{"", "x", true},
		{"latest", "latest", true},
		{"latest", "latest-2", false},
		{"azeno/*", "azeno/api", true},
		{"/^v\\d+$/", "v12", true},
		{"/^v\\d+$/", "v1.2", false},
	}
	for _, c := range cases {
		m, err := Pattern(c.p)
		if err != nil {
			t.Fatal(err)
		}
		if got := m(c.s); got != c.want {
			t.Errorf("Pattern(%q)(%q) = %v, want %v", c.p, c.s, got, c.want)
		}
	}
}

func TestSplitList(t *testing.T) {
	got := SplitList("latest, stable , /^v\\d{1,3}$/, release-*")
	want := []string{"latest", "stable", "/^v\\d{1,3}$/", "release-*"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SplitList = %#v, want %#v", got, want)
	}
}

func TestList(t *testing.T) {
	m, err := List("latest, /^v\\d+$/")
	if err != nil {
		t.Fatal(err)
	}
	for s, want := range map[string]bool{"latest": true, "v3": true, "2026.10.01": false} {
		if m(s) != want {
			t.Errorf("List(%q) = %v, want %v", s, !want, want)
		}
	}
	empty, _ := List("")
	if empty("x") {
		t.Error("empty list should match nothing")
	}
}
