package git

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// ageTree sets every file under dir (its .git folder too) an hour back, as
// a clone nobody touched since the last command.
func ageTree(t *testing.T, dir string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, old, old)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestHandChangesKeepsGitStatusWhileTheCloneIsUnchanged (bug hunt H-215,
// devco:D-956): the pick-up of hand edits asks git status once while the
// clone's files, index and HEAD stay as they were, across commands (a read
// server runs one after another), and asks again as soon as a file is
// edited by hand, also when the edit keeps the file's size, or one is
// still fresh.
func TestHandChangesKeepsGitStatusWhileTheCloneIsUnchanged(t *testing.T) {
	b, g, ctx := clone(t, newRemote(t, true)), newGen(t), context.Background()
	r := g.row(newID(t), "first", hlc.HLC{})
	write(t, b, store.Change{Row: r})
	ageTree(t, b.Dir())
	// the first status takes the aged files' stat into the index (and
	// rewrites it); an hour later that index is old too
	if _, err := b.HandChanges(ctx); err != nil {
		t.Fatal(err)
	}
	files := b.statusFiles(ctx)
	if len(files) == 0 {
		t.Fatal("no index path")
	}
	old := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(files[0], old, old); err != nil {
		t.Fatal(err)
	}
	calls := countGit(t, b)
	for i := range 3 {
		rep, err := b.HandChanges(ctx)
		if err != nil || len(rep.Rows) != 0 {
			t.Fatalf("call %d: %+v %v", i, rep, err)
		}
	}
	if n := calls(" status "); n != 1 {
		t.Fatalf("three pick-ups over an unchanged clone ran git status %d times, want 1", n)
	}

	// a hand edit of the same size, settled: seen
	p := filepath.Join(b.Dir(), filepath.FromSlash(rootRowPath(r.ID)))
	edited := strings.Replace(string(r.Doc), "first", "fir5t", 1)
	if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	rep, err := b.HandChanges(ctx)
	if err != nil || len(rep.Rows) != 1 || rep.Rows[0].Kind != HandModified {
		t.Fatalf("the hand edit was not seen: %+v %v", rep, err)
	}
	if n := calls(" status "); n != 2 {
		t.Fatalf("git status ran %d times after a hand edit, want 2", n)
	}

	// a file written just now is never answered from the kept status
	if err := os.WriteFile(filepath.Join(b.Dir(), "notes.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		rep, err = b.HandChanges(ctx)
		if err != nil || len(rep.Strays) != 1 {
			t.Fatalf("the fresh note was not seen: %+v %v", rep, err)
		}
	}
	if n := calls(" status "); n != 4 {
		t.Fatalf("git status ran %d times over a fresh file, want 4 (every call)", n)
	}
}

// TestStatusStampMissesEveryChange: the stamp a kept status stands on
// changes with each thing git status reads, and a stamp without a known
// index never matches.
func TestStatusStampMissesEveryChange(t *testing.T) {
	dir := t.TempDir()
	put := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	put("tree/a.json", "{}")
	index := put("index", "i")
	ignore := filepath.Join(dir, "tree", ".gitignore")
	ageTree(t, dir)
	in := stampInput{roots: []string{filepath.Join(dir, "tree")}, files: []string{ignore}, index: index, head: "c1"}
	now := time.Now()
	base := stampOf(in, now)
	if !base.settled {
		t.Fatal("an aged tree is not settled")
	}
	if again := stampOf(in, now); again != base {
		t.Fatal("the same tree stamped twice differs")
	}
	moved := in
	moved.head = "c2"
	wrote := in
	wrote.gen = 7
	for name, other := range map[string]stampInput{"HEAD": moved, "a write of this process": wrote} {
		if stampOf(other, now).tree == base.tree {
			t.Errorf("%s moved, the stamp did not", name)
		}
	}
	old := time.Now().Add(-10 * time.Minute)
	aPath := filepath.Join(dir, "tree", "a.json")
	sameTime := func() {
		info, err := os.Stat(aPath)
		if err != nil {
			t.Fatal(err)
		}
		put("tree/a.json", `{"k":1}`)
		if err := os.Chtimes(aPath, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name   string
		change func()
	}{
		{"a new file", func() { put("tree/b.json", "{}") }},
		{"an ignore file", func() { put("tree/.gitignore", "*.tmp\n") }},
		{"a same-size edit", func() { put("tree/a.json", "[]"); _ = os.Chtimes(aPath, old.Add(time.Second), old.Add(time.Second)) }},
		{"a resize keeping the time", sameTime},
		{"a removed file", func() { _ = os.Remove(filepath.Join(dir, "tree", "b.json")) }},
		{"a new empty folder", func() { _ = os.Mkdir(filepath.Join(dir, "tree", "sub"), 0o755) }},
	} {
		before := stampOf(in, time.Now())
		c.change()
		_ = filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
			if err == nil && p != aPath {
				_ = os.Chtimes(p, old, old) // folders keep the time they had: only the change moves the stamp
			}
			return nil
		})
		if after := stampOf(in, time.Now()); after.tree == before.tree {
			t.Errorf("%s left the stamp as it was", c.name)
		}
	}
	put("index", "ii")
	if after := stampOf(in, time.Now()); after.settled || after.index == base.index {
		t.Errorf("an index written just now: settled %v, index sum unchanged %v", after.settled, after.index == base.index)
	}
	unknown := in
	unknown.index = ""
	if stampOf(unknown, now).settled {
		t.Error("a stamp with no index is settled")
	}
}
