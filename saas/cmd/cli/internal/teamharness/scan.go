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
	objects := filepath.Join(c.Dir, ".git", "objects")
	err := filepath.WalkDir(c.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == objects {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return err
		}
		for _, n := range needles {
			if n != "" && strings.Contains(string(b), n) {
				rel, _ := filepath.Rel(c.Dir, path)
				found = append(found, Finding{Needle: n, Where: rel})
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
