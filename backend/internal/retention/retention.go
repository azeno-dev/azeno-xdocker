// Package retention decides which images to keep and which to delete. It is
// pure: it never talks to Docker or a registry, so the live preview in the UI
// and the scheduled run use exactly the same code.
package retention

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/azeno/xdocker/internal/match"
)

// Period is the bucket size for "keep N per period".
type Period string

const (
	Day   Period = "day"
	Week  Period = "week"
	Month Period = "month"
	Year  Period = "year"
)

// Action is the decision for one item.
type Action string

const (
	Keep   Action = "keep"
	Delete Action = "delete"
)

// Item is one local image tag or one registry tag.
type Item struct {
	Repo    string    `json:"repo"`
	Tag     string    `json:"tag"`
	Digest  string    `json:"digest,omitempty"`
	Created time.Time `json:"created"`
	Size    int64     `json:"size"`
	UsedBy  []string  `json:"usedBy,omitempty"`
}

// Ref returns repo:tag.
func (i Item) Ref() string { return i.Repo + ":" + i.Tag }

// Policy holds the rules. Zero values are safe: no protection, keep 1 newest
// per month.
type Policy struct {
	RepoPattern   string         `json:"repoPattern"`
	TagInclude    string         `json:"tagInclude"` // comma list; empty = all tags
	TagExclude    string         `json:"tagExclude"` // comma list of protected tags
	Period        Period         `json:"period"`
	KeepPerPeriod int            `json:"keepPerPeriod"`
	KeepMode      string         `json:"keepMode"` // newest | oldest
	KeepLastN     int            `json:"keepLastN"`
	MinAge        time.Duration  `json:"minAge"`
	MaxAge        time.Duration  `json:"maxAge"` // 0 = no limit
	SkipInUse     bool           `json:"skipInUse"`
	DateSource    string         `json:"dateSource"` // created | tag
	DateRegex     string         `json:"dateRegex"`  // capture group 1 holds the date
	DateLayout    string         `json:"dateLayout"` // Go layout, e.g. 2006.01.02
	Location      *time.Location `json:"-"`
}

// Decision is the outcome for one item.
type Decision struct {
	Item
	Ref    string    `json:"ref"`
	Date   time.Time `json:"date"`
	Bucket string    `json:"bucket"`
	Action Action    `json:"action"`
	Reason string    `json:"reason"`
}

// Result is a full plan.
type Result struct {
	Decisions  []Decision `json:"decisions"`
	KeepCount  int        `json:"keepCount"`
	DeleteSize int64      `json:"deleteSize"`
	// DeleteCount counts decisions with Action Delete.
	DeleteCount int `json:"deleteCount"`
}

// Validate checks the policy and fills defaults.
func (p *Policy) Validate() error {
	switch p.Period {
	case "":
		p.Period = Month
	case Day, Week, Month, Year:
	default:
		return fmt.Errorf("unknown period %q", p.Period)
	}
	if p.KeepPerPeriod < 1 {
		p.KeepPerPeriod = 1
	}
	switch p.KeepMode {
	case "":
		p.KeepMode = "newest"
	case "newest", "oldest":
	default:
		return fmt.Errorf("unknown keep mode %q", p.KeepMode)
	}
	if p.KeepLastN < 0 {
		return fmt.Errorf("keep last N must not be negative")
	}
	if p.MinAge < 0 || p.MaxAge < 0 {
		return fmt.Errorf("ages must not be negative")
	}
	if p.MaxAge > 0 && p.MaxAge < p.MinAge {
		return fmt.Errorf("max age must be larger than min age")
	}
	if p.DateSource == "" {
		p.DateSource = "created"
	}
	if p.DateSource == "tag" {
		if p.DateRegex == "" || p.DateLayout == "" {
			return fmt.Errorf("date from tag needs a regex and a layout")
		}
		if _, err := regexp.Compile(p.DateRegex); err != nil {
			return fmt.Errorf("date regex: %w", err)
		}
	}
	if p.Location == nil {
		p.Location = time.Local
	}
	for _, spec := range []string{p.RepoPattern} {
		if _, err := match.Pattern(spec); err != nil {
			return err
		}
	}
	for _, spec := range []string{p.TagInclude, p.TagExclude} {
		if _, err := match.List(spec); err != nil {
			return err
		}
	}
	return nil
}

// Plan applies the policy to items. Items outside the policy's scope
// (repository or tag-include filters) are left out of the result entirely.
//
// Per repository:
//  1. protected items are kept: excluded tags, in-use images (when
//     SkipInUse), and items younger than MinAge;
//  2. the KeepLastN newest items are kept;
//  3. items are grouped into period buckets; each bucket keeps items until it
//     holds KeepPerPeriod kept items (protected tags do not count, they are
//     usually aliases such as "latest");
//  4. keepers from steps 2-3 older than MaxAge are deleted;
//  5. everything else is deleted.
func Plan(p Policy, items []Item, now time.Time) (Result, error) {
	if err := p.Validate(); err != nil {
		return Result{}, err
	}
	repoM, _ := match.Pattern(p.RepoPattern)
	inclM := match.All
	if strings.TrimSpace(p.TagInclude) != "" {
		inclM, _ = match.List(p.TagInclude)
	}
	exclM, _ := match.List(p.TagExclude)
	var dateRe *regexp.Regexp
	if p.DateSource == "tag" {
		dateRe = regexp.MustCompile(p.DateRegex)
	}

	byRepo := map[string][]*Decision{}
	var repos []string
	for _, it := range items {
		if it.Tag == "" || it.Tag == "<none>" || !repoM(it.Repo) || !inclM(it.Tag) {
			continue
		}
		d := &Decision{Item: it, Ref: it.Ref(), Date: itemDate(it, p, dateRe)}
		d.Bucket = bucket(d.Date.In(p.Location), p.Period)
		if _, ok := byRepo[it.Repo]; !ok {
			repos = append(repos, it.Repo)
		}
		byRepo[it.Repo] = append(byRepo[it.Repo], d)
	}
	sort.Strings(repos)

	var res Result
	for _, repo := range repos {
		ds := byRepo[repo]
		sort.SliceStable(ds, func(i, j int) bool {
			if !ds[i].Date.Equal(ds[j].Date) {
				return ds[i].Date.After(ds[j].Date)
			}
			return ds[i].Tag > ds[j].Tag
		})
		planRepo(p, ds, now, exclM)
		for _, d := range ds {
			res.Decisions = append(res.Decisions, *d)
			if d.Action == Delete {
				res.DeleteCount++
				res.DeleteSize += d.Size
			} else {
				res.KeepCount++
			}
		}
	}
	return res, nil
}

const (
	sticky   = 1 // kept regardless of MaxAge
	counted  = 2 // kept and counts toward the bucket quota
	excluded = 4 // protected tag: kept, not counted
)

func planRepo(p Policy, ds []*Decision, now time.Time, exclM match.Matcher) {
	flags := make([]int, len(ds))
	for i, d := range ds {
		switch {
		case exclM(d.Tag):
			d.Action, d.Reason, flags[i] = Keep, "protected tag", sticky|excluded
		case p.SkipInUse && len(d.UsedBy) > 0:
			d.Action, d.Reason, flags[i] = Keep, "in use by "+strings.Join(d.UsedBy, ", "), sticky|counted
		case now.Sub(d.Date) < p.MinAge:
			d.Action, d.Reason, flags[i] = Keep, fmt.Sprintf("younger than %s", humanDuration(p.MinAge)), sticky|counted
		}
	}
	n := 0
	for i, d := range ds {
		if flags[i]&excluded != 0 {
			continue
		}
		if n >= p.KeepLastN {
			break
		}
		n++
		if d.Action == "" {
			d.Action, d.Reason, flags[i] = Keep, fmt.Sprintf("one of the last %d", p.KeepLastN), counted
		}
	}

	var order []string
	idx := map[string][]int{}
	for i, d := range ds {
		if _, ok := idx[d.Bucket]; !ok {
			order = append(order, d.Bucket)
		}
		idx[d.Bucket] = append(idx[d.Bucket], i)
	}
	for _, b := range order {
		is := append([]int(nil), idx[b]...)
		if p.KeepMode == "oldest" {
			for l, r := 0, len(is)-1; l < r; l, r = l+1, r-1 {
				is[l], is[r] = is[r], is[l]
			}
		}
		kept := 0
		for _, i := range is {
			if flags[i]&counted != 0 {
				kept++
			}
		}
		for _, i := range is {
			if ds[i].Action == "" && kept < p.KeepPerPeriod {
				ds[i].Action, ds[i].Reason, flags[i] = Keep, fmt.Sprintf("%s in %s", p.KeepMode, b), counted
				kept++
			}
		}
	}

	for i, d := range ds {
		if d.Action == Keep && flags[i]&sticky == 0 && p.MaxAge > 0 && now.Sub(d.Date) > p.MaxAge {
			d.Action, d.Reason = Delete, fmt.Sprintf("older than %s", humanDuration(p.MaxAge))
		}
		if d.Action == "" {
			d.Action = Delete
			d.Reason = fmt.Sprintf("%s already has %d kept", d.Bucket, p.KeepPerPeriod)
		}
	}
}

func itemDate(it Item, p Policy, re *regexp.Regexp) time.Time {
	if re != nil {
		if m := re.FindStringSubmatch(it.Tag); len(m) > 1 {
			if t, err := time.ParseInLocation(p.DateLayout, m[1], p.Location); err == nil {
				return t
			}
		}
	}
	return it.Created
}

func bucket(t time.Time, p Period) string {
	switch p {
	case Day:
		return t.Format("2006-01-02")
	case Week:
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	case Year:
		return t.Format("2006")
	default:
		return t.Format("2006-01")
	}
}

func humanDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days >= 60 && days%30 == 0:
		return fmt.Sprintf("%d months", days/30)
	case days >= 1 && d%(24*time.Hour) == 0:
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	default:
		return d.String()
	}
}
