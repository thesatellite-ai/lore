package git

// A read right after the process's own write (the write's render refresh,
// devco:D-921) reuses what the command already knows instead of asking git
// again (devco:D-945, bug hunt H-203): the data folder's status in a code
// repo, the roster replay of the write's own commit in a store clone. The
// answers stay git's: the rows read are the files written, and the replay
// equals a full one.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestRepoReadAfterOwnWriteRunsNoSecondStatus: in one command, a read
// after lore's own write of a committed row and a new row asks git for the
// data folder's status no second time, and still returns both files'
// new content (the written paths are read from disk, as git would have
// listed them changed).
func TestRepoReadAfterOwnWriteRunsNoSecondStatus(t *testing.T) {
	dir, g := newRepo(t), newGen(t)
	ctx := store.WithReadScope(context.Background())
	b, err := OpenRepo(ctx, dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a, c := g.row(newID(t), "a", hlc.HLC{}), g.row(newID(t), "c", hlc.HLC{})
	write(t, b, store.Change{Row: a}, store.Change{Row: c})
	gitT(t, dir, "add", ".lore/data")
	gitT(t, dir, "commit", "--quiet", "-m", "rows")
	calls := countGit(t, b)
	if rows, _, err := b.Read(ctx, ""); err != nil || len(rows) != 2 {
		t.Fatalf("first read: %d rows, %v", len(rows), err)
	}
	if n := calls("status"); n != 1 {
		t.Fatalf("the first read asked git status %d times, want 1", n)
	}
	a2 := g.row(a.ID, "a2 by lore", a.HLC)
	n := g.row(newID(t), "new by lore", hlc.HLC{})
	if err := b.Write(ctx, []store.Change{{Row: a2, Expect: a.HLC}, {Row: n}}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := b.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := calls("status"); got != 1 {
		t.Errorf("the read after the command's own write asked git status again (%d times in all, want 1)", got)
	}
	want := map[ids.AnyID][]byte{a.ID: a2.Doc, c.ID: c.Doc, n.ID: n.Doc}
	if len(rows) != len(want) {
		t.Fatalf("read after the write: %d rows, want %d", len(rows), len(want))
	}
	for _, r := range rows {
		if !bytes.Equal(r.Doc, want[r.ID]) {
			t.Errorf("row %s is not the file lore wrote:\n got  %s\n want %s", r.ID, r.Doc, want[r.ID])
		}
	}
	// a hand edit after lore's write is still seen by the next command
	// (another ReadScope asks git again)
	h := g.row(c.ID, "c by hand", c.HLC)
	if err := os.WriteFile(filepath.Join(dir, ".lore", "data", filepath.FromSlash(rowfs.RowPath(memories, c.ID))), h.Doc, 0o644); err != nil {
		t.Fatal(err)
	}
	next, _, err := b.Read(store.WithReadScope(context.Background()), "")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(next, func(r store.Row) bool { return r.ID == c.ID })
	if i < 0 || !bytes.Equal(next[i].Doc, h.Doc) {
		t.Error("the next command did not read the hand edit")
	}
}

// TestCloneOwnCommitReplayedWithoutLog: after a write commits to a store
// clone and lists its files (a command that prints its write lines), the
// roster replay held by the process is carried over that commit without
// `git log`, and the result is the full replay's.
func TestCloneOwnCommitReplayedWithoutLog(t *testing.T) {
	b := clone(t, newRemote(t, true))
	g := newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "first", hlc.HLC{})})
	authorityNow(t, b) // held at the first write's commit
	var listed []string
	ctx := store.WithCommitted(context.Background(), func(_ string, paths []string) { listed = append(listed, paths...) })
	second := g.row(newID(t), "second", hlc.HLC{})
	if err := b.Write(ctx, []store.Change{{Row: second}}); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(listed, func(p string) bool { return strings.HasSuffix(p, "/"+rowfs.RowPath(memories, second.ID)) }) {
		t.Fatalf("the commit's files were not reported: %v", listed)
	}
	calls := countGit(t, b)
	next := authorityNow(t, b)
	if n := calls("log "); n != 0 {
		t.Errorf("the replay asked git log %d time(s) for the commit this process made, want 0", n)
	}
	sameAuthority(t, next, fullReplay(t, b))
}
