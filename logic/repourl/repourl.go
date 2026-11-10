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
	"math"
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

// Removed says what Strip dropped from a URL. It never carries the
// dropped text (D25: the warning names the remote and the clean URL only).
type Removed string

// Removed values (SECRETS_AND_REFERENCES_DESIGN 14.3).
const (
	RemovedNone Removed = "none"
	// RemovedUser: a plain user name (https://mira@host) was dropped. Not
	// a secret, so no warning (14.4).
	RemovedUser Removed = "user"
	// RemovedPassword: the password part of the user info was dropped.
	RemovedPassword Removed = "password"
	// RemovedTokenUser: a token-shaped user name was dropped (GitHub and
	// others accept https://<token>@host/...).
	RemovedTokenUser Removed = "token_user"
)

// Warn reports whether the removal is a credential the person must hear
// about (14.4): a password or a token-shaped user name.
func (r Removed) Warn() bool { return r == RemovedPassword || r == RemovedTokenUser }

// tokenUserPrefixes are user names that are always tokens (14.6; the
// same list the url_token_user scanner rule keys on).
var tokenUserPrefixes = []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-", "x-access-token", "x-oauth-basic", "oauth2"}

// Token-shaped user name without a known prefix: at least this long and
// at least this Shannon entropy (bits/char), the url_token_user rule's
// thresholds (14.6).
const (
	tokenUserMinLen  = 20
	tokenUserEntropy = 3.5
)

// TokenShaped reports whether a URL user name looks like a token (14.6).
func TokenShaped(user string) bool {
	low := strings.ToLower(user)
	for _, p := range tokenUserPrefixes {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return len(user) >= tokenUserMinLen && entropy(user) >= tokenUserEntropy
}

// entropy is the Shannon entropy of s in bits per byte.
func entropy(s string) float64 {
	if s == "" {
		return 0
	}
	counts := map[byte]int{}
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	var e float64
	n := float64(len(s))
	for _, c := range counts {
		p := float64(c) / n
		e -= p * math.Log2(p)
	}
	return e
}

// ErrUnreadable is returned when lore cannot tell where a credential in a
// value ends (14.3: refused, E_URL_CREDENTIALS at the caller).
var ErrUnreadable = errors.New("repourl: the URL cannot be read, and it may hold a credential")

// Strip returns raw in the form lore may store and what it removed (D24,
// one normaliser for every URL lore stores).
//
// URL CREDENTIALS FIX / SQ13: an http or https URL (and any scheme other
// than ssh) loses its whole user part (a token is often the user name);
// an ssh:// URL keeps the login user and loses a password (a token-shaped
// login user is dropped whole, OV4); a scp-like
// URL (git@host:o/r) keeps its login user. A value without a scheme that
// has an "@" before its first "/" and is not scp-like is refused
// (ErrUnreadable), so a token is never stored by accident. Host, port,
// path, query, fragment and case are kept. Idempotent.
func Strip(raw string) (clean string, removed Removed, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", RemovedNone, ErrEmpty
	}
	if !strings.Contains(s, schemeSep) {
		head, _, _ := strings.Cut(s, "/")
		if at := strings.LastIndex(head, "@"); at >= 0 {
			if !isSCPLike(s) || strings.Contains(head[:at], ":") {
				return "", RemovedNone, ErrUnreadable
			}
		}
		return s, RemovedNone, nil
	}
	u, perr := url.Parse(s)
	if perr != nil {
		if strings.Contains(s, "@") {
			return "", RemovedNone, ErrUnreadable
		}
		return "", RemovedNone, fmt.Errorf("repourl: %q is not a URL", s)
	}
	if u.User == nil {
		return s, RemovedNone, nil
	}
	_, hasPw := u.User.Password()
	switch strings.ToLower(u.Scheme) {
	case "ssh", "git+ssh", "ssh+git":
		switch {
		case TokenShaped(u.User.Username()):
			// OV4: a token as the ssh login user is a credential too
			// (14.6); the whole user part goes.
			u.User = nil
			removed = RemovedTokenUser
		case hasPw:
			u.User = url.User(u.User.Username())
			removed = RemovedPassword
		default:
			return s, RemovedNone, nil
		}
	default:
		switch {
		case hasPw:
			removed = RemovedPassword
		case TokenShaped(u.User.Username()):
			removed = RemovedTokenUser
		default:
			removed = RemovedUser
		}
		u.User = nil
	}
	return u.String(), removed, nil
}

// StripCredentials is Strip reporting only whether anything was removed
// (the callers that predate Removed). New callers use Strip so a plain
// user name is dropped without a warning (14.4).
func StripCredentials(raw string) (clean string, stripped bool, err error) {
	clean, r, err := Strip(raw)
	return clean, r != RemovedNone, err
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

// urlFieldNames and urlFieldSuffixes mark a row field or config key as a
// URL by name (D27: the class-sweep test and the engine's write-time strip
// use the same rule, so a new "*_url" field is stripped without a code
// change).
var (
	urlFieldNames    = []string{"url", "origin", "remote", "origin_url", "base_url"}
	urlFieldSuffixes = []string{"_url", "_remote"}
)

// URLFieldName reports whether a field name marks a URL (D27).
func URLFieldName(name string) bool {
	n := strings.ToLower(name)
	for _, x := range urlFieldNames {
		if n == x {
			return true
		}
	}
	for _, x := range urlFieldSuffixes {
		if strings.HasSuffix(n, x) {
			return true
		}
	}
	return false
}
