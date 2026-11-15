// Package foldercase answers "is this folder in the repo, spelled exactly
// so?" on case-sensitive and case-insensitive file systems alike.
//
// Why: macOS (APFS, HFS+) and Windows ignore case. After a case-only folder
// rename (src/Charts to src/charts) a plain os.Stat of the old spelling
// still finds the folder, so a folder scope at src/Charts looked present,
// its rules quietly stopped matching the renamed files, and nothing said so
// (devco D-414); `lore scope add dir:SRC/charts` was taken as the folder
// src/charts and stored a spelling no file path ever matches. A folder
// counts here only when every name of its path is listed by its parent
// exactly as written.
//
// Whether names fold is detected once per Checker, from the repo top: the
// .git entry is stat'ed as .GIT too (read only, nothing created, so read
// commands stay read only). On a case-sensitive file system IsDir is one
// stat, as before; only a folding one pays a listing per path name.
//
//	New(view, top) ─▶ stat top/.git and top/.GIT: same file? ─▶ folds
//	IsDir(rel) ─▶ stat ─▶ (folds) each name listed exactly by its parent?
package foldercase

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// FS is the file-system view a Checker reads. Why an interface: the guard
// tests inject a case-folding view (Folding) on Linux, so the gate there
// runs the macOS path.
type FS interface {
	// Stat is os.Stat.
	Stat(path string) (fs.FileInfo, error)
	// Names lists the entry names of dir as the file system stores them.
	Names(dir string) ([]string, error)
}

// OS is the real file system.
type OS struct{}

// Stat is os.Stat.
func (OS) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }

// Names lists dir's entry names.
func (OS) Names(dir string) ([]string, error) {
	f, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read only
	return f.Readdirnames(-1)
}

// Checker checks folders under one repo top.
type Checker struct {
	view  FS
	top   string
	folds bool
}

// New is a Checker for the folders under top, read through view (OS{} in
// the product). It probes once whether names fold there.
func New(view FS, top string) Checker {
	return Checker{view: view, top: top, folds: probeFolds(view, top)}
}

// Folds reports whether the file system at top ignores case.
func (c Checker) Folds() bool { return c.folds }

// IsDir reports whether rel (slash-separated, relative to top) is an
// existing folder spelled exactly as rel spells it.
func (c Checker) IsDir(rel string) bool {
	rel = strings.Trim(rel, "/")
	fi, err := c.view.Stat(filepath.Join(c.top, filepath.FromSlash(rel)))
	if err != nil || !fi.IsDir() {
		return false
	}
	if !c.folds || rel == "" {
		return true
	}
	dir := c.top
	for _, name := range strings.Split(rel, "/") {
		names, err := c.view.Names(dir)
		if err != nil {
			return false
		}
		if !containsExact(names, name) {
			return false
		}
		dir = filepath.Join(dir, name)
	}
	return true
}

func containsExact(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// gitEntry is the entry every repo top holds (a folder, or a file in a
// linked worktree): the probe stats it in another case.
const gitEntry = ".git"

// probeFolds reports whether names fold at top: top/.git and top/.GIT are
// the same file. Without a .git it tries top itself in another case; with
// neither it answers false (case-sensitive, a plain stat as before).
func probeFolds(view FS, top string) bool {
	if same(view, filepath.Join(top, gitEntry), filepath.Join(top, strings.ToUpper(gitEntry))) {
		return true
	}
	base := filepath.Base(top)
	if other := swapCase(base); other != base {
		return same(view, top, filepath.Join(filepath.Dir(top), other))
	}
	return false
}

// same reports whether a and b both exist and are one file.
func same(view FS, a, b string) bool {
	fa, err := view.Stat(a)
	if err != nil {
		return false
	}
	fb, err := view.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}

// swapCase turns every upper-case letter of s to lower case and the other
// way round.
func swapCase(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsUpper(r) {
			return unicode.ToLower(r)
		}
		return unicode.ToUpper(r)
	}, s)
}

// Folding is a view of the real file system that ignores case the way APFS
// does: each name of a path resolves to the entry its parent lists equal
// under case folding (the exact spelling first). Why it exists: the Linux
// gate runs the case-insensitive paths with it (D-414); Names stays the
// real listing, as on a Mac.
type Folding struct{}

// Stat is os.Stat of path with each name resolved case-insensitively.
func (Folding) Stat(path string) (fs.FileInfo, error) {
	return os.Stat(foldPath(path))
}

// Names lists dir (resolved case-insensitively) as the file system stores
// the names.
func (Folding) Names(dir string) ([]string, error) { return OS{}.Names(foldPath(dir)) }

// foldPath spells each name of the absolute path p as its parent lists it,
// matching case-insensitively when the exact name is not listed.
func foldPath(p string) string {
	vol := filepath.VolumeName(p)
	rest := strings.Trim(p[len(vol):], string(filepath.Separator))
	cur := vol + string(filepath.Separator)
	if rest == "" {
		return cur
	}
	for _, name := range strings.Split(rest, string(filepath.Separator)) {
		pick := name
		if names, err := (OS{}).Names(cur); err == nil && !containsExact(names, name) {
			for _, n := range names {
				if strings.EqualFold(n, name) {
					pick = n
					break
				}
			}
		}
		cur = filepath.Join(cur, pick)
	}
	return cur
}
