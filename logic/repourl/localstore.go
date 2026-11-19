package repourl

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// LocalStoreHost is the host part NormalizeStore gives a store that is a
// git repository on this machine (/srv/git/notes.git). Such a url has no
// host; the leading underscore keeps it apart from every real DNS host, so
// a local repository never shares an id with a hosted one. It equals the
// git backend's git.LocalHost, so both name one store the same way.
const LocalStoreHost = "_local"

// NormalizeStore is Normalize for a store url: a hosted remote normalises
// as Normalize does; a plain absolute path to a git repository on this
// machine (the way `git clone /srv/git/notes.git` reaches it) is
// LocalStoreHost/<path without .git>. Normalize itself keeps refusing a
// local path, because a repo's origin there cannot identify the repo
// across clones; a store url is an address, and a local one is valid.
func NormalizeStore(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if fileuri.IsDrivePath(s) {
		s = fileuri.DriveURLPath(s) // C:\x is a local path (B3-27)
	}
	if !IsLocalStorePath(s) {
		return Normalize(s)
	}
	rel := strings.TrimSuffix(strings.Trim(path.Clean(s), "/"), gitSuffix)
	if rel == "" || strings.Contains(s, "/../") || strings.HasSuffix(s, "/..") {
		return "", fmt.Errorf("repourl: %q is not an absolute repository path", s)
	}
	return LocalStoreHost + "/" + rel, nil
}

// LocalRemoteKey is the classification-only name of a git remote that is a
// path on this machine (/srv/git/acme/tools.git, file:///srv/git/acme/tools.git):
// LocalStoreHost/<path without .git>, the spelling NormalizeStore gives the
// same path as a store. ok is false for anything else (a hosted remote, a
// relative path: the caller makes it absolute first).
//
// Why (devco D-706): Normalize refuses a local path because it cannot
// identify a repo across clones, so a repo whose only remote is a path was
// one no own: or repos: pattern could name, and it stayed a client repo by
// the fail-closed default with no way out. The key is used only to match
// own:/repos: patterns (and to print the own: command that names it),
// never as a clone or fetch address, a link key or a project id.
func LocalRemoteKey(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(s), "file://") {
		u, err := url.Parse(s)
		if err != nil || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) {
			return "", false
		}
		s = u.Path
	}
	if !IsLocalStorePath(s) {
		return "", false
	}
	n, err := NormalizeStore(s)
	if err != nil || !strings.HasPrefix(n, LocalStoreHost+"/") {
		return "", false
	}
	return n, true
}

// IsLocalStorePath reports a store url written as a plain absolute path
// (no scheme, no query): a git repository on this machine.
func IsLocalStorePath(s string) bool {
	if fileuri.IsDrivePath(s) {
		return !strings.ContainsAny(s, "?#")
	}
	return strings.HasPrefix(s, "/") && !strings.Contains(s, schemeSep) && !strings.ContainsAny(s, "?#")
}

// ShownStore is a store id (NormalizeStore) as a person reads it: a hosted
// store's host/owner/repo as is, a local one as its path ("/srv/git/notes",
// "C:/git/notes") rather than the internal LocalStoreHost key (B3-30).
// Messages that have the url the person typed show that instead.
func ShownStore(norm string) string {
	rest, ok := strings.CutPrefix(norm, LocalStoreHost+"/")
	if !ok {
		return norm
	}
	if fileuri.IsDrivePath(rest) {
		return rest
	}
	return "/" + rest
}

// IsLocalRemote reports a git remote that is a path on this machine,
// written the way git and lore's own messages quote one: a file:// url,
// an absolute path (/srv/git/x.git, C:\\git\\x), or one starting with ./,
// ../ or ~ (O-13). A bare word (github.com, kit.git) is not: lore reads it
// as an address and Normalize decides. Such a remote names a folder, not
// the repo across clones, so it is no address lore records: git remote
// set-url alone moves it.
func IsLocalRemote(raw string) bool {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(s), "file://") || fileuri.IsDrivePath(s) {
		return true
	}
	if strings.Contains(s, schemeSep) {
		return false
	}
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "~/")
}
