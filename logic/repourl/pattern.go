// pattern.go — the repo patterns of ~/.lore/config.yaml (`own:`, `repos:`,
// repo_orgs keys), a core copy of v0.1.17's repourl.Compile/Match so org
// resolution in the core (logic/orgresolve, FA-OLDCODE-1) matches exactly
// what the release binary matches.
//
// Patterns use `*` for exactly one path segment and `**` for any number of
// segments (zero or more). Inside a segment `*` is any run of characters
// that stays in that segment (`client-*`, `*-api`; devco D-453: an org
// listed its client repos one by one because only a whole-segment `*`
// was accepted). Nothing else is special: a pattern reads as a path.

package repourl

import (
	"fmt"
	"strings"
)

// Pattern wildcards. Named so the matcher and the validator agree.
const (
	// WildSegment matches exactly one path segment.
	WildSegment = "*"
	// WildAny matches zero or more path segments.
	WildAny = "**"
)

// Pattern is a validated repo pattern such as `github.com/acme/*`.
// The zero value matches nothing.
type Pattern struct {
	raw  string
	segs []string
}

// String returns the pattern as written (after normalisation of the host).
func (p Pattern) String() string { return p.raw }

// Compile validates and normalises a pattern. The host segment may not be a
// wildcard (a pattern for "any host" would match every repo of every
// company), `**` may appear at most once and only as a whole segment, and
// any other segment is a literal that may hold `*` (any run of characters
// within the segment).
func Compile(raw string) (Pattern, error) {
	s := strings.Trim(strings.TrimSpace(raw), "/")
	if s == "" {
		return Pattern{}, ErrEmpty
	}
	if strings.Contains(s, schemeSep) || isSCPLike(s) {
		// A pasted remote URL is an exact pattern.
		n, err := Normalize(s)
		if err != nil {
			return Pattern{}, err
		}
		s = n
	}
	s = strings.TrimSuffix(s, gitSuffix)
	segs := strings.Split(s, "/")
	if len(segs) < 2 {
		return Pattern{}, fmt.Errorf("repourl: pattern %q needs a host and at least one path segment", raw)
	}
	segs[0] = strings.ToLower(segs[0])
	if strings.Contains(segs[0], WildSegment) {
		return Pattern{}, fmt.Errorf("repourl: pattern %q may not use a wildcard for the host", raw)
	}
	anyCount := 0
	for _, seg := range segs {
		switch {
		case seg == "":
			return Pattern{}, fmt.Errorf("repourl: pattern %q has an empty segment", raw)
		case seg == WildAny:
			anyCount++
		case seg == WildSegment:
		case strings.Contains(seg, WildAny):
			return Pattern{}, fmt.Errorf("repourl: pattern %q: %q mixes text and **; ** stands for whole segments only (use * inside a segment, like client-*)", raw, seg)
		}
	}
	if anyCount > 1 {
		return Pattern{}, fmt.Errorf("repourl: pattern %q may use ** only once", raw)
	}
	return Pattern{raw: strings.Join(segs, "/"), segs: segs}, nil
}

// Match reports whether the normalised remote (output of Normalize) matches.
func (p Pattern) Match(normalized string) bool {
	if len(p.segs) == 0 || normalized == "" {
		return false
	}
	return matchSegs(p.segs, strings.Split(normalized, "/"))
}

// matchSegs matches pattern segments against value segments; `**` absorbs
// zero or more segments.
func matchSegs(pat, val []string) bool {
	for i, ps := range pat {
		if ps == WildAny {
			rest := pat[i+1:]
			for k := 0; k <= len(val); k++ {
				if matchSegs(rest, val[k:]) {
					return true
				}
			}
			return false
		}
		if len(val) == 0 {
			return false
		}
		if !segMatch(ps, val[0]) {
			return false
		}
		val = val[1:]
	}
	return len(val) == 0
}

// segMatch matches one pattern segment against one value segment: `*`
// alone is any segment, a `*` inside a segment any run of characters
// (also none), every other character itself.
func segMatch(ps, v string) bool {
	if ps == WildSegment {
		return true
	}
	parts := strings.Split(ps, WildSegment)
	if len(parts) == 1 {
		return ps == v
	}
	if !strings.HasPrefix(v, parts[0]) {
		return false
	}
	v = v[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(v, mid)
		if i < 0 {
			return false
		}
		v = v[i+len(mid):]
	}
	return len(v) >= len(last) && strings.HasSuffix(v, last)
}
