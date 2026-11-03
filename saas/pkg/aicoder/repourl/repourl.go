// Package repourl turns git remote URLs into one comparable form and matches
// them against the repo patterns used in ~/.lore/config.yaml (`own:`,
// `repos:`).
//
// Why one form: the same repo is reached as git@github.com:acme/app.git,
// ssh://git@github.com/acme/app, https://user@GitHub.com/acme/app.git and so
// on. Org resolution (LORE_TEAM_SPEC F10) must treat all of them as the same
// repo, so every remote is reduced to `host/path` — lowercase host, no
// scheme, no user, no port, no `.git` suffix, no trailing slash — before any
// comparison. The path keeps its case: hosts such as GitLab are
// case-sensitive in paths, and a pattern author writes what the host shows.
//
// Patterns use `*` for exactly one path segment and `**` for any number of
// segments (zero or more). Nothing else is special: there are no character
// classes and no partial-segment wildcards, so a pattern reads as a path.
//
// This package is a mechanism only: it knows nothing about orgs or config.
package repourl

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Pattern wildcards. Named so the matcher and the validator agree.
const (
	// WildSegment matches exactly one path segment.
	WildSegment = "*"
	// WildAny matches zero or more path segments.
	WildAny = "**"
	// gitSuffix is stripped from the last segment; hosts accept both forms.
	gitSuffix = ".git"
	// schemeSep separates a URL scheme from the rest.
	schemeSep = "://"
)

// hostRe is a DNS name or IPv4 address: what a git remote's host can be.
// It refuses "~" and other shell shorthands that would otherwise parse as a
// host in the bare `host/path` form.
var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// ErrEmpty is returned for an empty remote or pattern.
var ErrEmpty = errors.New("repourl: empty value")

// Normalize reduces a git remote URL to `host/path` (see the package
// comment). It accepts scheme URLs (https, http, ssh, git, file is refused),
// scp-like `user@host:path`, and an already-normalised `host/path`.
//
// Invariant: Normalize(Normalize(x)) == Normalize(x).
func Normalize(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ErrEmpty
	}
	var host, path string
	switch {
	case strings.Contains(s, schemeSep):
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("repourl: parse %q: %w", raw, err)
		}
		if u.Scheme == "file" || u.Host == "" {
			return "", fmt.Errorf("repourl: %q has no host (local remotes cannot identify a repo)", raw)
		}
		host, path = u.Hostname(), u.Path
	case isSCPLike(s):
		// user@host:owner/repo — the colon separates host from path.
		at := strings.LastIndex(s[:strings.Index(s, ":")], "@")
		rest := s[at+1:]
		colon := strings.Index(rest, ":")
		host, path = rest[:colon], rest[colon+1:]
	default:
		// Already "host/path" (as written in config patterns and links), or a
		// local filesystem path, which has no host and is refused.
		if strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") {
			return "", fmt.Errorf("repourl: %q is a local path, not a remote", raw)
		}
		slash := strings.Index(s, "/")
		if slash <= 0 {
			return "", fmt.Errorf("repourl: %q has no path", raw)
		}
		host, path = s[:slash], s[slash+1:]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		if c := strings.Index(host, ":"); c >= 0 {
			host = host[:c]
		}
	}
	host = strings.ToLower(strings.TrimSpace(host))
	// Strip exactly one leading and one trailing slash so an empty segment
	// inside the path ("//app") is still seen and refused below.
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimSuffix(path, gitSuffix)
	path = strings.TrimSuffix(path, "/")
	if host == "" || path == "" {
		return "", fmt.Errorf("repourl: %q has no host or path", raw)
	}
	if !hostRe.MatchString(host) {
		return "", fmt.Errorf("repourl: %q: %q is not a host name", raw, host)
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("repourl: %q has an empty or relative path segment", raw)
		}
	}
	return host + "/" + path, nil
}

// isSCPLike reports git's scp-like syntax: a colon before the first slash,
// and the part before the colon is not a single letter (a Windows drive).
func isSCPLike(s string) bool {
	colon := strings.Index(s, ":")
	if colon <= 0 {
		return false
	}
	slash := strings.Index(s, "/")
	if slash >= 0 && slash < colon {
		return false
	}
	return colon > 1
}

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
// company), `**` may appear at most once, and a segment is either a literal
// or exactly a wildcard.
func Compile(raw string) (Pattern, error) {
	s := strings.Trim(strings.TrimSpace(raw), "/")
	if s == "" {
		return Pattern{}, ErrEmpty
	}
	if strings.Contains(s, schemeSep) || isSCPLike(s) {
		// Allow users to paste a remote URL as an exact pattern.
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
	if segs[0] == WildSegment || segs[0] == WildAny {
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
		case strings.Contains(seg, WildSegment):
			return Pattern{}, fmt.Errorf("repourl: pattern %q: %q mixes text and a wildcard; use * or ** as a whole segment", raw, seg)
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

func matchSegs(pat, val []string) bool {
	for i, ps := range pat {
		if ps == WildAny {
			rest := pat[i+1:]
			// ** may absorb zero or more segments.
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
		if ps != WildSegment && ps != val[0] {
			return false
		}
		val = val[1:]
	}
	return len(val) == 0
}
