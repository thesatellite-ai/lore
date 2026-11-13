package repourl

import (
	"fmt"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"path"
	"strings"
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

// IsLocalStorePath reports a store url written as a plain absolute path
// (no scheme, no query): a git repository on this machine.
func IsLocalStorePath(s string) bool {
	if fileuri.IsDrivePath(s) {
		return !strings.ContainsAny(s, "?#")
	}
	return strings.HasPrefix(s, "/") && !strings.Contains(s, schemeSep) && !strings.ContainsAny(s, "?#")
}
