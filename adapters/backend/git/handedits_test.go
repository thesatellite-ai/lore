package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestHandChangesListsRowFilesAndStrays: an edited, a new and a deleted row
// file are listed with their kind and the committed bytes; a note and a
// stray folder are strays; lore's own files are neither.
func TestHandChangesListsRowFilesAndStrays(t *testing.T) {
	t.Parallel()
	b, g, ctx := clone(t, newRemote(t, true)), newGen(t), context.Background()
	edited, gone := g.row(newID(t), "edited", hlc.HLC{}), g.row(newID(t), "gone", hlc.HLC{})
	write(t, b, store.Change{Row: edited}, store.Change{Row: gone})
	added := g.row(newID(t), "new", hlc.HLC{})
	dir := b.Dir()
	put := func(rel string, body []byte) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(rootRowPath(edited.ID), []byte(strings.Replace(string(edited.Doc), "edited", "changed by hand", 1)))
	put(rootRowPath(added.ID), added.Doc)
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(rootRowPath(gone.ID)))); err != nil {
		t.Fatal(err)
	}
	put("notes.txt", []byte("mine\n"))
	put("drafts/idea.md", []byte("idea\n"))
	rep, err := b.HandChanges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]HandKind{}
	for _, c := range rep.Rows {
		kinds[c.ID.String()] = c.Kind
		if c.Folder != rowfs.FolderRoot || c.Table != memories {
			t.Errorf("%s: folder %q table %q", c.Path, c.Folder, c.Table)
		}
		if (c.Kind == HandAdded) != (c.Base == nil) {
			t.Errorf("%s (%s): committed bytes %q", c.Path, c.Kind, c.Base)
		}
	}
	want := map[string]HandKind{edited.ID.String(): HandModified, added.ID.String(): HandAdded, gone.ID.String(): HandDeleted}
	if len(kinds) != len(want) {
		t.Fatalf("rows = %+v, want %v", rep.Rows, want)
	}
	for id, k := range want {
		if kinds[id] != k {
			t.Errorf("%s is %v, want %v", id, kinds[id], k)
		}
	}
	if strings.Join(rep.Strays, ",") != "drafts/idea.md,notes.txt" {
		t.Errorf("strays = %v", rep.Strays)
	}
}

// TestSyncSetsHandEditsAsideAndPutsThemBack: a tracked file still changed
// by hand neither stops a sync nor is merged or lost: it is stashed for
// the merge and back afterwards, while the remote's commit arrives.
func TestSyncSetsHandEditsAsideAndPutsThemBack(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	a, other, g := clone(t, remote, WithMerger(newestMerger{})), clone(t, remote, WithMerger(newestMerger{})), newGen(t)
	mine, theirs := g.row(newID(t), "mine", hlc.HLC{}), g.row(newID(t), "theirs", hlc.HLC{})
	write(t, a, store.Change{Row: mine})
	syncOK(t, a)
	syncOK(t, other)
	write(t, other, store.Change{Row: theirs})
	syncOK(t, other)
	p := filepath.Join(a.Dir(), filepath.FromSlash(rootRowPath(mine.ID)))
	hand := strings.Replace(string(mine.Doc), "mine", "broken by hand", 1) + "<<<<<<< left\n"
	if err := os.WriteFile(p, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	syncOK(t, a)
	if got, err := os.ReadFile(p); err != nil || string(got) != hand {
		t.Fatalf("the hand edit was not put back after the sync: %q %v", got, err)
	}
	get(t, a, theirs.ID) // the remote's row arrived
	if out := gitT(t, a.Dir(), "stash", "list"); out != "" {
		t.Errorf("a stash was left behind: %s", out)
	}
}

// TestRestoreHeadAndPutBack: a deleted file comes back as committed and
// goes again; a new file goes and comes back with the person's bytes.
func TestRestoreHeadAndPutBack(t *testing.T) {
	t.Parallel()
	b, g, ctx := clone(t, newRemote(t, true)), newGen(t), context.Background()
	r := g.row(newID(t), "kept", hlc.HLC{})
	write(t, b, store.Change{Row: r})
	p := filepath.Join(b.Dir(), filepath.FromSlash(rootRowPath(r.ID)))
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	rep, err := b.HandChanges(ctx)
	if err != nil || len(rep.Rows) != 1 {
		t.Fatalf("rows = %+v %v", rep.Rows, err)
	}
	if err := b.RestoreHead(ctx, rep.Rows[0]); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != string(r.Doc) {
		t.Fatalf("restored %q, want the committed row", got)
	}
	if err := PutBack(rep.Rows[0], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("PutBack(nil) left the file: %v", err)
	}
	if err := PutBack(rep.Rows[0], []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != "{}" {
		t.Fatalf("PutBack wrote %q", got)
	}
}

// TestUnstageKeepsFiles: lore's next commit never carries a file a person
// staged; the file itself stays as it is.
func TestUnstageKeepsFiles(t *testing.T) {
	t.Parallel()
	b, g, ctx := clone(t, newRemote(t, true)), newGen(t), context.Background()
	r := g.row(newID(t), "a", hlc.HLC{})
	write(t, b, store.Change{Row: r})
	if err := os.WriteFile(filepath.Join(b.Dir(), "notes.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, b.Dir(), "add", "notes.txt")
	if err := b.Unstage(ctx); err != nil {
		t.Fatal(err)
	}
	if staged := gitT(t, b.Dir(), "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("still staged: %s", staged)
	}
	if _, err := os.Stat(filepath.Join(b.Dir(), "notes.txt")); err != nil {
		t.Fatal(err)
	}
}

// TestHandChangesLeavesTopRowsToTheLayoutCommit (D-ad): a row file at the
// store's top (the first plain layout's place of the root folder) is no
// stray and no hand edit: the layout commit moves it into _store/.
func TestHandChangesLeavesTopRowsToTheLayoutCommit(t *testing.T) {
	t.Parallel()
	b, g, ctx := clone(t, newRemote(t, true)), newGen(t), context.Background()
	r := g.row(newID(t), "at the top", hlc.HLC{})
	writeFile(t, filepath.Join(b.Dir(), filepath.FromSlash(rowfs.RowPath(memories, r.ID))), string(r.Doc))
	writeFile(t, filepath.Join(b.Dir(), "notes.txt"), "mine\n")
	rep, err := b.HandChanges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 0 || len(rep.Strays) != 1 || rep.Strays[0] != "notes.txt" {
		t.Fatalf("rows %+v strays %v, want only notes.txt as a stray", rep.Rows, rep.Strays)
	}
	if !NeedsFlattenAt(b.Dir()) {
		t.Fatal("a row at the store's top does not ask for the layout commit")
	}
}
