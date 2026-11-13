package wire

// storelocalpath.go — a store url that names a place on this machine:
// a plain path (/srv/git/notes.git, ~/git/notes.git) or a file:// url.
// A bare git repository there is a git store, the same as
// `git clone /srv/git/notes.git` reaches it: lore clones it under
// ~/.lore/stores and pushes to it. Any other folder is a folder store,
// spelled file:// (as before), and so is a checkout (a folder with .git):
// git refuses a push to its checked-out branch, so as a git store its
// first commit would already fail; as a folder it keeps working as it
// always did. Before this, a plain path was a bare
// "has no scheme" error and a file:// url of a bare repository was refused
// with a hint to map a fake host name onto it in the global git config.
//
// The written form keeps the two kinds apart by spelling alone, so a url
// reads the same on every later open, whatever the folder holds then: a
// git store is the plain absolute path, a folder store file://.

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
)

// bareRepoMarkers are what every bare git repository holds at its top
// (git's own layout: the HEAD file and the objects folder).
var bareRepoMarkers = []struct {
	name string
	dir  bool
}{{"HEAD", false}, {"objects", true}}

// isLocalGitRepo reports whether dir is a bare git repository (HEAD and
// objects/ at its top) lore can clone and push to. A folder that is
// already a lore folder store (lore-store.yaml at its top) is not: it
// stays the folder store it was made as.
func isLocalGitRepo(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, rowfs.MarkerFile)); err == nil {
		return false
	}
	for _, m := range bareRepoMarkers {
		st, err := os.Stat(filepath.Join(dir, m.name))
		if err != nil || st.IsDir() != m.dir {
			return false
		}
	}
	return true
}

// isPlainLocalPath reports a store url typed as a path, not a url: it
// starts with "/" or "~" (a shell-style home path) and has no "://".
func isPlainLocalPath(raw string) bool {
	if strings.Contains(raw, "://") {
		return false
	}
	return strings.HasPrefix(raw, "/") || raw == homeTilde || strings.HasPrefix(raw, homeTilde+"/") || filepath.IsAbs(raw) ||
		fileuri.IsDrivePath(raw) // C:\x on every OS (B3-27): a path, never scheme "c"
}

// missingPathError is E_BAD_PATH for a store path that is not a folder on
// this machine: lore cannot tell which kind of store was meant, so the
// hint names both spellings.
func missingPathError(p string) error {
	return errs.New(errs.BadPath, "store: "+p+" does not exist (or is not a folder)").
		WithHint("for a git store give the path of an existing git repository (/path/to/repo.git, made with `git init --bare`); for a plain folder store give file:///path/to/folder (lore makes the folder)")
}

// localPathURL is the written store url of a plain path typed as a store
// url: the cleaned absolute path for a git repository (a git store), the
// file:// url for any other existing folder (a checkout too) (a folder store). A path that
// does not exist is E_BAD_PATH naming both spellings, since lore cannot
// tell which the person meant.
func localPathURL(raw string) (string, error) {
	p := raw
	if p == homeTilde || strings.HasPrefix(p, homeTilde+"/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errs.Wrap(errs.BadPath, "store: ~ in a store path needs a home folder", err)
		}
		p = filepath.Join(home, strings.TrimPrefix(p, homeTilde))
	}
	if fileuri.IsDrivePath(p) && runtime.GOOS != windowsGOOS {
		return "", missingPathError(p) // a drive path names no folder here
	}
	p, err := filepath.Abs(p)
	if err != nil {
		return "", errs.Wrap(errs.BadPath, "store: the store path", err)
	}
	st, err := os.Stat(p)
	if err != nil || !st.IsDir() {
		return "", missingPathError(p)
	}
	if isLocalGitRepo(p) {
		return filepath.ToSlash(p), nil
	}
	return fileuri.URL(p, ""), nil
}

// gitRepoOfFileURL is the plain path of the git repository a cleaned
// file:// url names, and true; "" and false when its folder is not one
// (missing, a plain folder, or already a lore folder store), which leaves
// the url a folder store.
func gitRepoOfFileURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" {
		return "", false
	}
	dir := fileuri.OSPath(u.Path)
	if !isLocalGitRepo(dir) {
		return "", false
	}
	return filepath.ToSlash(filepath.Clean(dir)), true
}
