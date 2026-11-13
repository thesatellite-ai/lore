package wire

// strayclone.go — another folder under ~/.lore/stores that is a clone of
// the same store (A-16). lore keeps one clone of a store at a fixed path
// (stores/<owner>/<repo>); a person who renames that folder gets a fresh
// clone at the old path on the next open, and the renamed folder (with
// anything only it holds) was left behind without a word. When lore sets
// a clone up (the moment it gives the clone its git identity) it now
// names every other clone of that store it finds there.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
)

// strayDepth is how deep under stores/ a clone sits (<owner>/<repo>, or a
// third level for nested owners).
const strayDepth = 3

// strayCloneLines are the notices for the other clones of dir's store
// under the same stores/ folder: none when dir is not under one (a
// person's own clone), has no origin, or is the only clone.
func strayCloneLines(dir string) []string {
	root := storesRootOf(dir)
	origin := git.OriginAt(dir)
	if root == "" || origin == "" {
		return nil
	}
	want, err := git.RemoteOfRaw(origin)
	if err != nil {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || !d.IsDir() || p == root {
			return nil // an unreadable folder is not ours to report
		}
		if p == dir || d.Name() == ".git" {
			return filepath.SkipDir
		}
		if o := git.OriginAt(p); o != "" {
			if r, rerr := git.RemoteOfRaw(o); rerr == nil && r.Norm == want.Norm && !otherBranchClone(root, want, dir, p) {
				out = append(out, "store clone "+underHome(p)+" is also a clone of this store (a renamed or copied folder?); lore reads and writes only "+underHome(dir)+": copy what you need from it, then delete it")
			}
			return filepath.SkipDir // a clone's own folders hold no other clone
		}
		if rel, _ := filepath.Rel(root, p); strings.Count(rel, string(filepath.Separator))+1 >= strayDepth {
			return filepath.SkipDir
		}
		return nil
	})
	return out
}

// otherBranchClone reports whether p is lore's own clone of the store for
// another branch than dir: the default clone or a branch-pinned one
// (<repo>@<branch>, git.DefaultCloneDir). A store used on main and pinned
// to staging has both folders on purpose; neither is a renamed copy, and
// telling the person to delete one would lose the clone lore uses.
func otherBranchClone(root string, want git.Remote, dir, p string) bool {
	home := filepath.Dir(root) // DefaultCloneDir puts clones at <home>/stores/...
	for _, branch := range []string{"", headBranch(p)} {
		def, err := git.DefaultCloneDir(home, want, branch)
		if err == nil && sameFolder(def, p) && !sameFolder(def, dir) {
			return true
		}
	}
	return false
}

// storesRootOf is the stores/ folder dir sits in, "" when none.
func storesRootOf(dir string) string {
	for p := filepath.Dir(dir); p != filepath.Dir(p); p = filepath.Dir(p) {
		if filepath.Base(p) == git.StoresDir {
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				return p
			}
			return ""
		}
	}
	return ""
}
