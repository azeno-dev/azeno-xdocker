package retention

import (
	"sort"
	"strings"
	"testing"
	"time"
)

var bucharest, _ = time.LoadLocation("Europe/Bucharest")

func day(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, bucharest)
	if err != nil {
		panic(err)
	}
	return t.Add(12 * time.Hour)
}

func items(repo string, dates ...string) []Item {
	var out []Item
	for _, d := range dates {
		out = append(out, Item{Repo: repo, Tag: strings.ReplaceAll(d, "-", "."), Created: day(d), Size: 100})
	}
	return out
}

func tagsWith(r Result, a Action) []string {
	var out []string
	for _, d := range r.Decisions {
		if d.Action == a {
			out = append(out, d.Tag)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

func eq(t *testing.T, name string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// The example from PLAN.md section 4.6.
func TestPlanExample(t *testing.T) {
	its := items("myapp/api", "2026-10-05", "2026-10-03", "2026-10-01", "2026-09-28", "2026-09-15", "2026-09-02", "2026-08-30", "2026-08-10")
	its = append(its, Item{Repo: "myapp/api", Tag: "latest", Created: day("2026-10-05")})
	p := Policy{RepoPattern: "myapp/*", Period: Month, KeepPerPeriod: 1, KeepLastN: 3, TagExclude: "latest", Location: bucharest}
	r, err := Plan(p, its, day("2026-10-07"))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "keep", tagsWith(r, Keep), []string{"latest", "2026.10.05", "2026.10.03", "2026.10.01", "2026.09.28", "2026.08.30"})
	eq(t, "delete", tagsWith(r, Delete), []string{"2026.09.15", "2026.09.02", "2026.08.10"})
	if r.DeleteSize != 300 || r.DeleteCount != 3 {
		t.Errorf("delete size/count = %d/%d", r.DeleteSize, r.DeleteCount)
	}
}

// With keep-last 1 the current month must still hold only one image: the
// quota counts images already kept by keep-last.
func TestQuotaCountsKeepLast(t *testing.T) {
	its := items("a", "2026-10-05", "2026-10-03", "2026-09-20", "2026-09-10")
	r, _ := Plan(Policy{Period: Month, KeepPerPeriod: 1, KeepLastN: 1, Location: bucharest}, its, day("2026-10-30"))
	eq(t, "keep", tagsWith(r, Keep), []string{"2026.10.05", "2026.09.20"})
}

func TestOldestMode(t *testing.T) {
	its := items("a", "2026-09-28", "2026-09-15", "2026-09-02")
	r, _ := Plan(Policy{Period: Month, KeepPerPeriod: 1, KeepMode: "oldest", Location: bucharest}, its, day("2026-12-01"))
	eq(t, "keep", tagsWith(r, Keep), []string{"2026.09.02"})
}

func TestKeepPerPeriod(t *testing.T) {
	its := items("a", "2026-09-28", "2026-09-15", "2026-09-02", "2026-08-01")
	r, _ := Plan(Policy{Period: Month, KeepPerPeriod: 2, Location: bucharest}, its, day("2026-12-01"))
	eq(t, "keep", tagsWith(r, Keep), []string{"2026.09.28", "2026.09.15", "2026.08.01"})
}

func TestMinAgeProtects(t *testing.T) {
	its := items("a", "2026-10-06", "2026-10-05", "2026-10-01")
	r, _ := Plan(Policy{Period: Month, KeepPerPeriod: 1, MinAge: 7 * 24 * time.Hour, Location: bucharest}, its, day("2026-10-07"))
	eq(t, "delete", tagsWith(r, Delete), nil)
	if !strings.Contains(r.Decisions[0].Reason, "younger than 7 days") {
		t.Errorf("reason = %q", r.Decisions[0].Reason)
	}
}

func TestInUseProtectedAndCounted(t *testing.T) {
	its := items("a", "2026-09-28", "2026-09-15")
	its[1].UsedBy = []string{"api-prod"}
	r, _ := Plan(Policy{Period: Month, KeepPerPeriod: 1, SkipInUse: true, Location: bucharest}, its, day("2026-12-01"))
	eq(t, "keep", tagsWith(r, Keep), []string{"2026.09.15"})

	r, _ = Plan(Policy{Period: Month, KeepPerPeriod: 1, SkipInUse: false, Location: bucharest}, its, day("2026-12-01"))
	eq(t, "keep without skip", tagsWith(r, Keep), []string{"2026.09.28"})
}

func TestMaxAge(t *testing.T) {
	its := items("a", "2026-09-10", "2025-01-10", "2024-01-10")
	its[2].UsedBy = []string{"legacy"}
	p := Policy{Period: Month, KeepPerPeriod: 1, MaxAge: 365 * 24 * time.Hour, SkipInUse: true, Location: bucharest}
	r, _ := Plan(p, its, day("2026-10-07"))
	eq(t, "keep", tagsWith(r, Keep), []string{"2026.09.10", "2024.01.10"})
	eq(t, "delete", tagsWith(r, Delete), []string{"2025.01.10"})
}

func TestWeekBucketsAcrossYear(t *testing.T) {
	// 2026-12-31 (Thu) and 2027-01-01 (Fri) are both in ISO week 2026-W53.
	its := items("a", "2027-01-01", "2026-12-31", "2026-12-20")
	r, _ := Plan(Policy{Period: Week, KeepPerPeriod: 1, Location: bucharest}, its, day("2027-03-01"))
	eq(t, "keep", tagsWith(r, Keep), []string{"2027.01.01", "2026.12.20"})
	if r.Decisions[0].Bucket != "2026-W53" {
		t.Errorf("bucket = %s", r.Decisions[0].Bucket)
	}
}

func TestBucketUsesTimeZone(t *testing.T) {
	// 2026-09-30 22:30 UTC is already 1 October in Bucharest.
	its := []Item{
		{Repo: "a", Tag: "late", Created: time.Date(2026, 9, 30, 22, 30, 0, 0, time.UTC)},
		{Repo: "a", Tag: "early", Created: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)},
	}
	r, _ := Plan(Policy{Period: Month, KeepPerPeriod: 1, Location: bucharest}, its, day("2026-12-01"))
	eq(t, "keep", tagsWith(r, Keep), []string{"late", "early"})
}

func TestScopeFilters(t *testing.T) {
	its := append(items("azeno/api", "2026-09-28", "2026-09-15"), items("other/x", "2026-09-28", "2026-09-15")...)
	its = append(its, Item{Repo: "azeno/api", Tag: "<none>", Created: day("2026-01-01")})
	r, _ := Plan(Policy{RepoPattern: "azeno/*", Period: Month, Location: bucharest}, its, day("2026-12-01"))
	if len(r.Decisions) != 2 {
		t.Fatalf("decisions = %d, want 2 (only azeno/*, no <none>)", len(r.Decisions))
	}
	r, _ = Plan(Policy{TagInclude: "2026.09.1*", Period: Month, Location: bucharest}, its, day("2026-12-01"))
	if len(r.Decisions) != 2 {
		t.Fatalf("tag include: decisions = %d, want 2", len(r.Decisions))
	}
}

func TestMultipleReposIndependent(t *testing.T) {
	its := append(items("a", "2026-09-28", "2026-09-15"), items("b", "2026-09-27", "2026-09-14")...)
	r, _ := Plan(Policy{Period: Month, Location: bucharest}, its, day("2026-12-01"))
	eq(t, "keep", tagsWith(r, Keep), []string{"2026.09.28", "2026.09.27"})
}

func TestDateFromTag(t *testing.T) {
	its := []Item{
		{Repo: "a", Tag: "build-2026.09.28", Created: day("2026-10-01")},
		{Repo: "a", Tag: "build-2026.08.15", Created: day("2026-10-01")},
	}
	p := Policy{Period: Month, DateSource: "tag", DateRegex: `(\d{4}\.\d{2}\.\d{2})`, DateLayout: "2006.01.02", Location: bucharest}
	r, err := Plan(p, its, day("2026-12-01"))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "keep", tagsWith(r, Keep), []string{"build-2026.09.28", "build-2026.08.15"})
}

func TestValidate(t *testing.T) {
	bad := []Policy{
		{Period: "decade"},
		{KeepMode: "random"},
		{KeepLastN: -1},
		{MinAge: 10 * time.Hour, MaxAge: time.Hour},
		{DateSource: "tag"},
		{RepoPattern: "/([/"},
	}
	for i, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}
