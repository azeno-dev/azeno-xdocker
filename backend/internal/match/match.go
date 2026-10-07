// Package match implements the search syntax shared by the UI, the bulk
// delete flows and retention policies:
//
//	text       plain text (substring for searches, exact for patterns)
//	glob*      wildcards: * matches any run of characters, ? one character
//	/regex/    a Go regular expression
package match

import (
	"fmt"
	"regexp"
	"strings"
)

// Matcher reports whether a string matches.
type Matcher func(s string) bool

// All matches everything.
func All(string) bool { return true }

// Search compiles a free-text search: plain text is a case-insensitive
// substring match; globs and /regex/ match the whole string.
func Search(q string) (Matcher, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return All, nil
	}
	if re, ok, err := asRegex(q); ok {
		return wrap(re), err
	}
	if hasGlob(q) {
		return wrap(globRegex(q, false)), nil
	}
	lq := strings.ToLower(q)
	return func(s string) bool { return strings.Contains(strings.ToLower(s), lq) }, nil
}

// Pattern compiles a policy pattern: plain text must match exactly
// (case-insensitive), globs and /regex/ as in Search. An empty pattern
// matches everything.
func Pattern(p string) (Matcher, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return All, nil
	}
	if re, ok, err := asRegex(p); ok {
		return wrap(re), err
	}
	return wrap(globRegex(p, true)), nil
}

// List compiles a comma-separated list of patterns; a string matches when any
// pattern does. An empty list matches nothing.
func List(spec string) (Matcher, error) {
	var ms []Matcher
	for _, part := range SplitList(spec) {
		m, err := Pattern(part)
		if err != nil {
			return nil, err
		}
		ms = append(ms, m)
	}
	return func(s string) bool {
		for _, m := range ms {
			if m(s) {
				return true
			}
		}
		return false
	}, nil
}

// SplitList splits a comma-separated list, keeping commas that appear inside
// /regex/ entries.
func SplitList(spec string) []string {
	var out []string
	var cur strings.Builder
	inRe := false
	for i, r := range spec {
		switch {
		case r == '/' && (cur.Len() == 0 || strings.TrimSpace(cur.String()) == "") && !inRe:
			inRe = true
		case r == '/' && inRe && (i == len(spec)-1 || nextNonSpace(spec[i+1:]) == ','):
			inRe = false
		case r == ',' && !inRe:
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func nextNonSpace(s string) rune {
	for _, r := range s {
		if r != ' ' && r != '\t' {
			return r
		}
	}
	return ','
}

func asRegex(q string) (*regexp.Regexp, bool, error) {
	if len(q) >= 2 && q[0] == '/' && q[len(q)-1] == '/' {
		re, err := regexp.Compile("(?i)" + q[1:len(q)-1])
		if err != nil {
			return nil, true, fmt.Errorf("invalid regex %s: %w", q, err)
		}
		return re, true, nil
	}
	return nil, false, nil
}

func hasGlob(s string) bool { return strings.ContainsAny(s, "*?") }

func globRegex(g string, exact bool) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range g {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	// A search glob without a tag part ("azeno/*") should still match
	// "azeno/api:1.0", so only anchor the end when the glob names a tag or
	// is an exact pattern.
	if exact || strings.Contains(g, ":") {
		b.WriteString("$")
	}
	return regexp.MustCompile(b.String())
}

func wrap(re *regexp.Regexp) Matcher {
	if re == nil {
		return func(string) bool { return false }
	}
	return re.MatchString
}
