// Package repourl is the core's git remote URL normaliser (layer L1, pure:
// stdlib only).
//
// Why a core copy: the v0.1.17 normaliser lives in saas/pkg/aicoder/repourl,
// and the core may not import the old modules (CODE ORG). Normalize keeps
// v0.1.17's exact output, because store links written by v0.1.17 hold that
// form and must keep matching.
//
// Three questions answer every URL comparison of repo identity (PROV1-3,
// PROVENANCE_DESIGN 10.3):
//
//   - Normalize: the comparison key, "host/path" (host lower-cased, scheme,
//     user, port and ".git" dropped, path case KEPT). Computed when needed,
//     never stored as the URL itself.
//   - StripCredentials: the form that may be stored (URL CREDENTIALS FIX,
//     SQ13): http/https lose the whole user part, ssh and scp-like keep the
//     login user and lose any password.
//   - CaseOnly: two keys that differ only in letter case (PROV3).
package repourl

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	gitSuffix = ".git"
	schemeSep = "://"
)

// hostRe is a DNS-ish host name (lower case, after folding).
var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// ErrEmpty is returned for a blank value.
var ErrEmpty = errors.New("repourl: empty value")

// Normalize turns a git remote (https://, ssh://, scp-like git@host:o/r,
// or an already-normalised host/path) into "host/path". A local path or a
// file:// remote is refused: it cannot identify a repo across clones.
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
			return "", fmt.Errorf("repourl: %q is not a URL", redact(raw))
		}
		if u.Scheme == "file" || u.Host == "" {
			return "", fmt.Errorf("repourl: %q has no host (local remotes cannot identify a repo)", redact(raw))
		}
		host, path = u.Hostname(), u.Path
	case isSCPLike(s):
		userHost, rest, _ := strings.Cut(s, ":")
		if at := strings.LastIndex(userHost, "@"); at >= 0 {
			userHost = userHost[at+1:]
		}
		host, path = userHost, rest
	default:
		if strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") {
			return "", fmt.Errorf("repourl: %q is a local path, not a remote", raw)
		}
		var found bool
		host, path, found = strings.Cut(s, "/")
		if !found || host == "" {
			return "", fmt.Errorf("repourl: %q has no path", redact(raw))
		}
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		if c := strings.Index(host, ":"); c >= 0 {
			host = host[:c]
		}
	}
	host = strings.ToLower(strings.TrimSpace(host))
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimSuffix(path, gitSuffix)
	path = strings.TrimSuffix(path, "/")
	if host == "" || path == "" {
		return "", fmt.Errorf("repourl: %q has no host or path", redact(raw))
	}
	if !hostRe.MatchString(host) {
		return "", fmt.Errorf("repourl: %q is not a host name", host)
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("repourl: %q has an empty or relative path segment", redact(raw))
		}
	}
	return host + "/" + path, nil
}

// isSCPLike reports git's scp-like form user@host:path (a colon before any
// slash, and not a one-letter Windows drive).
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

// StripCredentials returns raw in the form lore may store, and whether a
// credential was removed (the caller then prints the credential warning,
// never the value).
//
// URL CREDENTIALS FIX / SQ13: an http or https URL loses its whole user
// part (a token is often the user name); an ssh:// URL keeps the login user
// and loses a password; a scp-like URL (git@host:o/r) has no password field
// and is returned as is. A host/path value has nothing to strip. A value
// whose credential part cannot be found (unparsable with an "@" in it) is
// an error, so a token is never stored by accident.
func StripCredentials(raw string) (clean string, stripped bool, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false, ErrEmpty
	}
	if !strings.Contains(s, schemeSep) {
		return s, false, nil
	}
	u, perr := url.Parse(s)
	if perr != nil {
		if strings.Contains(s, "@") {
			return "", false, errors.New("repourl: the URL cannot be read, and it may hold a credential")
		}
		return "", false, fmt.Errorf("repourl: %q is not a URL", s)
	}
	if u.User == nil {
		return s, false, nil
	}
	switch strings.ToLower(u.Scheme) {
	case "ssh", "git+ssh", "ssh+git":
		if _, has := u.User.Password(); !has {
			return s, false, nil
		}
		u.User = url.User(u.User.Username())
	default:
		u.User = nil
	}
	return u.String(), true, nil
}

// CaseOnly reports whether two normalised keys differ only in letter case
// (PROV3: the same repo, confirmed once, never a fork).
func CaseOnly(a, b string) bool { return a != b && strings.EqualFold(a, b) }

// redact hides a possible user part of raw in an error message: errors are
// printed, and a remote may carry a token.
func redact(raw string) string {
	if c, _, err := StripCredentials(raw); err == nil {
		return c
	}
	return "<url>"
}
