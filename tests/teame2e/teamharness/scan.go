package teamharness

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Finding is one place a forbidden string was found.
type Finding struct {
	Needle string
	Where  string // a file path, or "git objects"
}

// LeakScan looks for needles (internal or personal text, internal ids, store
// names) everywhere a client could get them from this checkout (§6, F20):
//
//   - every file in the work tree, tracked, untracked AND ignored (a Docker
//     build context, a ZIP of the folder or an upload carries all of them);
//   - every plain file under .git except the object store (hooks, config,
//     .git/lore machine state, info/exclude);
//   - every git object, packed or loose (anything a push could carry).
func (c *Clone) LeakScan(needles ...string) []Finding {
	c.A.w.T.Helper()
	var found []Finding
	// Walked through an os.Root on the clone (P2-LINT-5); paths are
	// slash-separated and relative to it.
	const objects = ".git/objects"
	root, err := os.OpenRoot(c.Dir)
	if err != nil {
		c.A.w.T.Fatalf("leak scan %s: %v", c.Dir, err)
	}
	defer func() { _ = root.Close() }() // read-only use
	fsys := root.FS()
	err = fs.WalkDir(fsys, ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel == objects {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := fs.ReadFile(fsys, rel)
		if err != nil {
			return err
		}
		for _, n := range needles {
			if n != "" && strings.Contains(string(b), n) {
				found = append(found, Finding{Needle: n, Where: filepath.FromSlash(rel)})
			}
		}
		return nil
	})
	if err != nil {
		c.A.w.T.Fatalf("leak scan %s: %v", c.Dir, err)
	}
	for _, n := range needles {
		if where := objectsLocate(c.A.w, c.Dir, n); len(where) > 0 {
			found = append(found, Finding{Needle: n, Where: "git objects: " + strings.Join(where, "; ")})
		}
	}
	return found
}

// FetchEverything fetches every ref the remote has into the clone, the way a
// curious client would (`git fetch origin '+refs/*:refs/fetched/*'`), so a
// following LeakScan sees all of it. Hidden is not private (§2).
func (c *Clone) FetchEverything() {
	c.A.w.T.Helper()
	c.Git("fetch", "-q", "origin", "+refs/*:refs/fetched/*")
}
