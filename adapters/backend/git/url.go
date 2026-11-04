package git

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Url schemes the git backend answers (spec 7.2). The scp-like form
// `git@host:owner/repo` has no scheme; CanonicalURL turns it into ssh://.
const (
	SchemeSSH   = "ssh"
	SchemeHTTPS = "https"
	SchemeHTTP  = "http"
)

// Schemes lists every scheme NewOpener should be registered under.
var Schemes = []string{SchemeSSH, SchemeHTTPS, SchemeHTTP}

// Clone layout under the lore home (spec 6.1 Store clones,
// saas/pkg/aicoder/stores/clone.go:58).
const (
	// ClonesDir is the folder under the home holding every store clone.
	ClonesDir = "clones"
	// bareSuffix ends the bare clone folder: <host>/<owner>/<repo>.git.
	bareSuffix = ".git"
	// workDir is the worktree lore owns: <host>/<owner>/<repo>/work.
	workDir = "work"
	// lockName is the clone's lock file beside the worktree, outside it so
	// the lock never shows up in `git status` or a commit.
	lockName = "lock"
)

// gitSuffix is stripped from a repo path; hosts accept both forms.
const gitSuffix = ".git"

// hostPattern is a DNS name or IPv4 address (repourl.go hostRe): it refuses
// "~" and other shorthands that would parse as a host.
var hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// Remote is a parsed store url.
//
// Invariant: URL never holds a password or token (D24). Norm is
// `host/owner/repo` (lower-case host, no port, user or .git) and names the
// clone folder, so every form of one url shares one clone. URL is the
// canonical (ssh:// for the scp-like form) credential-free url; the clone's
// origin is the person's own spelling (OriginOf, WithOrigins), not URL.
type Remote struct {
	URL  string
	Norm string
}

// CanonicalURL rewrites a store url into the form store.Openers can parse
// and this backend writes: scp-like `user@host:path` becomes
// `ssh://user@host/path`, and every credential is stripped. Callers run it
// before Openers.Open, because net/url cannot parse the scp-like form.
func CanonicalURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if isSCPLike(s) {
		userHost, path, _ := strings.Cut(s, ":")
		user, host := "", userHost
		if at := strings.LastIndex(userHost, "@"); at >= 0 {
			user, host = userHost[:at+1], userHost[at+1:]
		}
		s = SchemeSSH + "://" + user + host + "/" + strings.TrimPrefix(path, "/")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errs.New(errs.StoreScheme, "git: the store url does not parse")
	}
	r, err := RemoteOf(u)
	if err != nil {
		return "", err
	}
	if u.RawQuery != "" {
		return r.URL + "?" + u.RawQuery, nil
	}
	return r.URL, nil
}

// isSCPLike reports git's scp-like syntax: a colon before the first slash,
// no "://", and more than one letter before the colon (not a drive letter).
func isSCPLike(s string) bool {
	if strings.Contains(s, "://") {
		return false
	}
	colon := strings.Index(s, ":")
	if colon <= 1 {
		return false
	}
	slash := strings.Index(s, "/")
	return slash < 0 || slash > colon
}

// RemoteOf validates a store url and strips its credentials: https and
// http lose their userinfo entirely (a user there is a token), ssh keeps
// the login name (it is not a secret) and loses any password. The query
// (the folder parameter) and fragment are not part of the remote.
func RemoteOf(u *url.URL) (Remote, error) {
	switch u.Scheme {
	case SchemeSSH, SchemeHTTPS, SchemeHTTP:
	default:
		return Remote{}, errs.New(errs.StoreScheme, "git: no git backend for scheme "+quoteScheme(u.Scheme))
	}
	host := strings.ToLower(u.Hostname())
	if !hostPattern.MatchString(host) {
		return Remote{}, errs.New(errs.StoreScheme, "git: the store url has no valid host")
	}
	path := strings.Trim(u.Path, "/")
	path = strings.TrimSuffix(path, gitSuffix)
	if path == "" {
		return Remote{}, errs.New(errs.StoreScheme, "git: the store url has no repository path")
	}
	for seg := range strings.SplitSeq(path, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, "-") {
			return Remote{}, errs.New(errs.StoreScheme, "git: the store url has an empty or relative path segment")
		}
	}
	clean := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	if u.Scheme == SchemeSSH && u.User != nil && u.User.Username() != "" {
		clean.User = url.User(u.User.Username())
	}
	return Remote{URL: clean.String(), Norm: host + "/" + path}, nil
}

// OriginOf is the url lore writes as a store clone's origin for the store
// url raw: the person's own spelling, as in stores.yaml or on the command
// line (as v0.1.17 wrote it), with every credential removed and without
// the query (?folder=, ?branch= are lore's, not git's). The spelling
// matters to git twice: url.<base>.insteadOf / pushInsteadOf rules match
// the url text, so a rule keyed on `git@github.com:` never applies to the
// equivalent ssh:// url (GDOCS-1); and the scp-like `host:srv/repo` is
// relative to the login's home while `ssh://host/srv/repo` is absolute, so
// rewriting one into the other points git at another repository.
// Invariant: the result never holds a password or token: an scp-like url
// cannot carry one (CanonicalURL validates it), a url form goes through
// RemoteOf, which strips it.
func OriginOf(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	canon, err := CanonicalURL(s)
	if err != nil {
		return "", err
	}
	if isSCPLike(s) {
		s, _, _ = strings.Cut(s, "?")
		if user, _, ok := strings.Cut(s, "@"); ok && strings.Contains(user, ":") {
			// user:password@host:path: keep only the login name.
			s = user[:strings.Index(user, ":")] + s[len(user):]
		}
		return s, nil
	}
	u, err := url.Parse(canon)
	if err != nil {
		return "", errs.New(errs.StoreScheme, "git: the store url does not parse")
	}
	r, err := RemoteOf(u)
	if err != nil {
		return "", err
	}
	return r.URL, nil
}

// quoteScheme renders a scheme for a message (schemes never hold secrets).
func quoteScheme(s string) string { return `"` + s + `"` }

// CloneBase is the clone folder stem for a remote under home:
// <home>/clones/<host>/<owner>/<repo>. The bare clone is CloneBase+".git"
// and the worktree CloneBase/work (spec 6.1).
func CloneBase(home string, r Remote) (string, error) {
	if !filepath.IsAbs(home) {
		return "", errs.New(errs.BadPath, "git: the lore home must be an absolute path")
	}
	return filepath.Join(home, ClonesDir, filepath.FromSlash(r.Norm)), nil
}
