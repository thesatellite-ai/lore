package probe

// folderstore.go — the store checks for a folder store (a file:// url
// whose folder is not a git repository; adapters/backend/folder reads and
// writes it as plain files). The git probes (ls-remote, push --dry-run,
// fetch of the marker) fail on such a folder with "does not appear to be
// a git repository", which made a supported personal store look broken
// in Doctor (2026-11-06). These read the folder itself instead.
//
//	store.read   ─▶ FolderRead   (list the folder)
//	store.write  ─▶ FolderWrite  (create + remove one temp file)
//	store.marker ─▶ FolderMarker (lore-store.yaml at the root)

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// Folder-store facts a git repository has and a folder store has not.
const (
	// fileScheme is a folder store's url scheme (adapters/backend/folder.Scheme).
	fileScheme = "file"
	// gitDirName is a work tree's git folder.
	gitDirName = ".git"
	// bareHead and bareObjects together mark a bare repository.
	bareHead    = "HEAD"
	bareObjects = "objects"
	// writeProbePattern names the write test's temp file (removed at once).
	writeProbePattern = ".lore-write-probe-*"
	// homeTildeHost is `file://~/x` (host "~"): a folder under the home.
	homeTildeHost = "~"
)

// FolderStorePath is the folder of a folder-store url: a file:// url with
// an absolute path (or ~ for the prober's home) whose folder is not a git
// repository, bare or not. A folder that does not exist yet counts (the
// read check then says so). ok is false for every other url, a file://
// url of a git repository included: those take the git probes.
func (p *Prober) FolderStorePath(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != fileScheme || u.User != nil || u.RawQuery != "" || u.Opaque != "" {
		return "", false
	}
	fileuri.FoldDriveHost(u) // file://C:/x is the drive path, not a host
	dir := u.Path
	switch {
	case u.Host == homeTildeHost:
		dir = expandHome("~"+dir, p.o.Home)
	case strings.HasPrefix(dir, "/"+homeTildeHost+"/") || dir == "/"+homeTildeHost:
		dir = expandHome(strings.TrimPrefix(dir, "/"), p.o.Home)
	case u.Host != "" && u.Host != "localhost":
		return "", false
	default:
		// "/C:/Users/a/x" names C:\Users\a\x on Windows; used as-is it
		// was `\C:\Users\a\x`, which Windows refuses.
		dir = fileuri.OSPath(dir)
	}
	if !filepath.IsAbs(dir) {
		return "", false
	}
	dir = filepath.Clean(dir)
	if isGitRepo(dir) {
		return "", false
	}
	return dir, true
}

// isGitRepo reports whether dir is a git work tree (.git) or a bare
// repository (HEAD file and objects folder).
func isGitRepo(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, gitDirName)); err == nil {
		return true
	}
	head, herr := os.Stat(filepath.Join(dir, bareHead))
	obj, oerr := os.Stat(filepath.Join(dir, bareObjects))
	return herr == nil && oerr == nil && head.Mode().IsRegular() && obj.IsDir()
}

// FolderRead is store.read for a folder store: the folder exists and can
// be listed. Classes: ok, no_access (missing, not a folder, unreadable).
func (p *Prober) FolderRead(dir string) Outcome {
	start := time.Now()
	st, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Outcome{Class: ClassNoAccess, Detail: "folder " + p.tilde(dir) + " does not exist", Took: time.Since(start)}
	case err != nil:
		return Outcome{Class: ClassNoAccess, Detail: Sanitize("cannot read " + p.tilde(dir) + ": " + reason(err)), Took: time.Since(start)}
	case !st.IsDir():
		return Outcome{Class: ClassNoAccess, Detail: p.tilde(dir) + " is a file, not a folder", Took: time.Since(start)}
	}
	if _, err := os.ReadDir(dir); err != nil {
		return Outcome{Class: ClassNoAccess, Detail: Sanitize("cannot read " + p.tilde(dir) + ": " + reason(err)), Took: time.Since(start)}
	}
	return Outcome{Class: ClassOK, Detail: "folder store " + p.tilde(dir) + " (no git): readable", Took: time.Since(start)}
}

// FolderCreatable is store.read and store.write for a folder store the
// person asked lore to CREATE: a folder that already exists is checked as
// FolderWrite does; a missing one is fine when its nearest existing parent
// is a folder lore can write in (one temp file made and removed there), as
// the save then makes it (folder.Init). Classes: ok, no_access, no_write.
func (p *Prober) FolderCreatable(dir string) Outcome {
	start := time.Now()
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		return p.FolderWrite(dir)
	}
	parent := filepath.Dir(dir)
	for {
		if _, err := os.Lstat(parent); !errors.Is(err, fs.ErrNotExist) || parent == filepath.Dir(parent) {
			break
		}
		parent = filepath.Dir(parent)
	}
	if o := p.FolderWrite(parent); o.Class != ClassOK {
		o.Detail = "cannot create " + p.tilde(dir) + ": " + o.Detail
		return o
	}
	return Outcome{Class: ClassOK, Detail: "folder store " + p.tilde(dir) + " (no git): does not exist yet; saving creates it in " + p.tilde(parent), Took: time.Since(start)}
}

// FolderWrite is store.write for a folder store: one temp file created
// and removed in the folder. Classes: ok, no_write.
func (p *Prober) FolderWrite(dir string) Outcome {
	start := time.Now()
	if o := p.FolderRead(dir); o.Class != ClassOK {
		return o
	}
	cannot := func(err error) Outcome {
		return Outcome{Class: ClassNoWrite, Detail: Sanitize("cannot write in " + p.tilde(dir) + ": " + reason(err)), Took: time.Since(start)}
	}
	f, err := os.CreateTemp(dir, writeProbePattern)
	if err != nil {
		return cannot(err)
	}
	name := f.Name()
	cerr := f.Close()
	if err := errors.Join(cerr, os.Remove(name)); err != nil {
		return cannot(err)
	}
	return Outcome{Class: ClassOK, Detail: "folder store " + p.tilde(dir) + " (no git): writable", Took: time.Since(start)}
}

// reason is err without the path a *fs.PathError repeats ("permission
// denied", not "open /home/…/.lore-write-probe-123: permission denied").
func reason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// FolderMarker is store.marker for a folder store: lore-store.yaml at the
// folder's root.
func (p *Prober) FolderMarker(dir string) MarkerResult {
	start := time.Now()
	if o := p.FolderRead(dir); o.Class != ClassOK {
		return MarkerResult{Outcome: o}
	}
	st, err := os.Stat(filepath.Join(dir, rowfs.MarkerFile))
	present := err == nil && st.Mode().IsRegular()
	return MarkerResult{Outcome: Outcome{Class: ClassOK, Took: time.Since(start)}, Present: present}
}
