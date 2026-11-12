package rowfs

// storetop.go — "am I inside a store?" (STORE layout 1.5, 2026-11-12). A
// store's checkout (a git store's clone, or a folder store's folder) is
// recognised by lore-store.yaml at its top. Lore never treats one as a
// code repo: no folder rule, no git wiring, no .lore/ of its own; a
// command run inside one works on the store folder it was run in.
//
//	StoreTop      ─▶ walk up from dir: the first folder holding .git ends
//	                 the walk (the git top: a store only when lore-store.yaml
//	                 sits beside .git); with no .git anywhere, the first
//	                 folder holding lore-store.yaml (a folder store)
//	StoreFolderAt ─▶ the nearest folder from dir up to the top that has a
//	                 project marker; the store root (".") otherwise

import (
	"os"
	"path/filepath"
	"strings"
)

// gitEntry is a git work tree's .git (a folder, or a pointer file).
const gitEntry = ".git"

// StoreTop reports whether dir is inside a store's checkout and returns
// its top folder (symlinks resolved) and whether it is a git clone. It
// only reads (a few stats on the way up).
func StoreTop(dir string) (top string, isGit bool, ok bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false, false
	}
	if real, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = real
	}
	folderTop := ""
	for d := abs; ; d = filepath.Dir(d) {
		hasMarker := isRegular(filepath.Join(d, MarkerFile))
		if _, serr := os.Lstat(filepath.Join(d, gitEntry)); serr == nil {
			if hasMarker {
				return d, true, true
			}
			return "", false, false // a code repo's top: not a store
		}
		if hasMarker && folderTop == "" {
			folderTop = d
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	if folderTop != "" {
		return folderTop, false, true
	}
	return "", false, false
}

// StoreFolderAt is the store folder dir sits in, inside the store whose
// top is top: the nearest folder from dir up to top (top excluded) whose
// project marker (the folder's _meta.json) exists, slash separated; the
// store root (FolderRoot) when there is none or dir is outside top.
func StoreFolderAt(top, dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return FolderRoot
	}
	if real, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = real
	}
	for d := abs; d != top && filepath.Dir(d) != d; d = filepath.Dir(d) {
		rel, rerr := filepath.Rel(top, d)
		if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return FolderRoot
		}
		name, cerr := CleanFolder(filepath.ToSlash(rel))
		if cerr != nil {
			continue
		}
		if isRegular(filepath.Join(top, filepath.FromSlash(LayoutStore.DataPath(name)), MetaFileName)) {
			return name
		}
	}
	return FolderRoot
}

// isRegular reports whether p is a regular file.
func isRegular(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
