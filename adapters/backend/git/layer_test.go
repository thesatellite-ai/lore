package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// layerKey is the layer of feat/x of one test repo in the root folder.
func layerKey(branch string) branchlayer.Key {
	return branchlayer.Key{StoreBranch: testBranch, Folder: rowfs.FolderRoot, RepoKey: branchlayer.RepoKey("example.com/acme/app"), Branch: branch}
}

// layerWrite writes changes onto key's layer and fails the test on error.
func layerWrite(t *testing.T, b *Backend, key branchlayer.Key, changes ...store.Change) LayerWriteResult {
	t.Helper()
	res, err := b.LayerWrite(context.Background(), LayerWriteRequest{Key: key, Changes: changes, Op: "memory.add",
		Facts: branchlayer.Facts{Repo: "example.com/acme/app", BaseBranch: "main", Split: "abc", CodeHead: "def", By: "act_1"}})
	if err != nil {
		t.Fatalf("layer write: %v", err)
	}
	return res
}

// layerRowsOf is the layer's changed rows by id.
func layerRowsOf(t *testing.T, b *Backend, key branchlayer.Key) map[string]store.Row {
	t.Helper()
	l, err := b.LayerOpen(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := b.LayerRows(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]store.Row{}
	for _, r := range rows {
		m[r.ID.String()] = r
	}
	return m
}

// TestLayerWriteNeverTouchesTheStoreBranch: a layer write is a commit on
// the hidden ref only; the store branch, its worktree and the remote stay
// as they were, and the layer sees the store branch's newer rows too.
func TestLayerWriteNeverTouchesTheStoreBranch(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b := clone(t, remote)
	g := newGen(t)
	shared := newID(t)
	write(t, b, store.Change{Row: g.row(shared, "on main", hlc.HLC{})})
	syncOK(t, b)
	head := gitT(t, b.Dir(), "rev-parse", "HEAD")
	key := layerKey("feat/x")
	mine := newID(t)
	res := layerWrite(t, b, key, store.Change{Row: g.row(mine, "branch only", hlc.HLC{})})
	if !res.Created || res.Layer.Base != head {
		t.Fatalf("first write: %+v (head %s)", res, head)
	}
	if got := gitT(t, b.Dir(), "rev-parse", "HEAD"); got != head {
		t.Fatalf("the store branch moved: %s -> %s", head, got)
	}
	if st := gitT(t, b.Dir(), "status", "--porcelain"); st != "" {
		t.Fatalf("the worktree changed: %q", st)
	}
	if _, err := b.Get(context.Background(), memories, mine); err == nil {
		t.Fatal("the store branch sees the branch's row")
	}
	if out := gitT(t, b.Dir(), "ls-remote", remote, "refs/lore/*"); out != "" {
		t.Fatalf("a write pushed: %s", out)
	}
	rows := layerRowsOf(t, b, key)
	if len(rows) != 1 || rows[mine.String()].ID != mine {
		t.Fatalf("layer rows: %v", rows)
	}
	// an edit of a row of the store branch lands in the layer only
	cur := get(t, b, shared)
	layerWrite(t, b, key, store.Change{Row: g.row(shared, "edited on the branch", cur.HLC), Expect: cur.HLC})
	if got := get(t, b, shared); !strings.Contains(string(got.Doc), "on main") {
		t.Fatalf("the store branch row changed: %s", got.Doc)
	}
	rows = layerRowsOf(t, b, key)
	if !strings.Contains(string(rows[shared.String()].Doc), "edited on the branch") {
		t.Fatalf("layer copy: %s", rows[shared.String()].Doc)
	}
	// a stale expect is refused
	_, err := b.LayerWrite(context.Background(), LayerWriteRequest{Key: key, Changes: []store.Change{{Row: g.row(shared, "late", cur.HLC), Expect: cur.HLC}}})
	if !errs.Has(err, errs.Stale) {
		t.Fatalf("stale layer write: %v", err)
	}
	l, err := b.LayerOpen(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if l.Facts.Branch != "feat/x" || l.Facts.Split != "abc" || l.Facts.Started.IsZero() || l.Base != head {
		t.Fatalf("facts: %+v base %s", l.Facts, l.Base)
	}
}

// TestLayerLandSquashesOnce: landing makes one single-parent commit on
// the store branch holding the layer's rows, pushes it, and moves the
// layer to the trash (remote too); landing again is "already landed".
func TestLayerLandSquashesOnce(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "seed", hlc.HLC{})})
	syncOK(t, b)
	key := layerKey("feat/x")
	a, c := newID(t), newID(t)
	layerWrite(t, b, key, store.Change{Row: g.row(a, "one", hlc.HLC{})})
	layerWrite(t, b, key, store.Change{Row: g.row(c, "two", hlc.HLC{})})
	if rep, err := b.LayerPush(context.Background()); err != nil || len(rep.Pushed) != 1 {
		t.Fatalf("push: %+v %v", rep, err)
	}
	before := gitT(t, b.Dir(), "rev-parse", "HEAD")
	res, err := b.LayerLand(context.Background(), LandRequest{Key: key, Subject: "lore: land feat/x (example.com/acme/app): 2 memories"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || !res.Pushed || res.Trash == "" {
		t.Fatalf("land: %+v", res)
	}
	// one landing commit on the first-parent line; its second parent is the
	// layer tip (the writers' own commits, for the authority replay)
	if parents := strings.Fields(gitT(t, b.Dir(), "log", "-1", "--format=%P", res.Commit)); len(parents) != 2 || parents[0] != before {
		t.Fatalf("landing parents %v, want [%s <layer tip>]", parents, before)
	}
	get(t, b, a)
	get(t, b, c)
	if out := gitT(t, b.Dir(), "ls-remote", remote, key.Ref()); out != "" {
		t.Fatalf("the layer ref is still on the remote: %s", out)
	}
	if out := gitT(t, b.Dir(), "ls-remote", remote, "refs/heads/main"); !strings.HasPrefix(out, res.Commit) {
		t.Fatalf("the landing was not pushed: %s", out)
	}
	if out := gitT(t, b.Dir(), "ls-remote", remote, res.Trash); out == "" {
		t.Fatal("no trash ref on the remote")
	}
	again, err := b.LayerLand(context.Background(), LandRequest{Key: key, Subject: "x"})
	if err != nil || again.AlreadyLanded != res.Commit {
		t.Fatalf("second land: %+v %v", again, err)
	}
	// restore brings the notes back as an open layer
	l, err := b.LayerRestore(context.Background(), key, time.Time{}, true)
	if err != nil || !l.Exists() {
		t.Fatalf("restore: %+v %v", l, err)
	}
	if rows := layerRowsOf(t, b, key); len(rows) != 2 {
		t.Fatalf("restored rows: %d", len(rows))
	}
}

// TestLayerLandConflictChangesNothing: the same text edited on the branch
// and on the store branch stops the landing with E_LAND_CONFLICT; the
// store branch, the worktree and the layer are as before.
func TestLayerLandConflictChangesNothing(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b := clone(t, remote, WithMerger(conflictMerger{}))
	g := newGen(t)
	id := newID(t)
	write(t, b, store.Change{Row: g.row(id, "base", hlc.HLC{})})
	syncOK(t, b)
	key := layerKey("feat/x")
	cur := get(t, b, id)
	layerWrite(t, b, key, store.Change{Row: g.row(id, "branch text", cur.HLC), Expect: cur.HLC})
	write(t, b, store.Change{Row: g.row(id, "main text", cur.HLC), Expect: cur.HLC})
	syncOK(t, b)
	head := gitT(t, b.Dir(), "rev-parse", "HEAD")
	l0, _ := b.LayerOpen(context.Background(), key)
	_, err := b.LayerLand(context.Background(), LandRequest{Key: key, Subject: "lore: land"})
	if !errs.Has(err, errs.LandConflict) || !strings.Contains(err.Error(), id.String()) {
		t.Fatalf("land: %v", err)
	}
	if got := gitT(t, b.Dir(), "rev-parse", "HEAD"); got != head {
		t.Fatal("the store branch moved")
	}
	if st := gitT(t, b.Dir(), "status", "--porcelain"); st != "" {
		t.Fatalf("worktree: %q", st)
	}
	if _, err := os.Stat(filepath.Join(b.Dir(), ".git", "SQUASH_MSG")); err == nil {
		t.Fatal("SQUASH_MSG left behind")
	}
	if l1, _ := b.LayerOpen(context.Background(), key); l1.Tip != l0.Tip {
		t.Fatal("the layer moved")
	}
}

// TestLayerTwoClonesShareAndMerge: a teammate's pushed layer is read from
// its fetched copy, written on, and two diverged copies merge on push.
func TestLayerTwoClonesShareAndMerge(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	write(t, a, store.Change{Row: g.row(newID(t), "seed", hlc.HLC{})})
	syncOK(t, a)
	syncOK(t, b)
	key := layerKey("feat/x")
	x := newID(t)
	layerWrite(t, a, key, store.Change{Row: g.row(x, "from a", hlc.HLC{})})
	if _, err := a.LayerPush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if off, err := b.LayerFetch(context.Background()); err != nil || off {
		t.Fatalf("fetch: %v %v", off, err)
	}
	if rows := layerRowsOf(t, b, key); len(rows) != 1 {
		t.Fatalf("b sees %d layer rows, want 1", len(rows))
	}
	y, z := newID(t), newID(t)
	layerWrite(t, b, key, store.Change{Row: g.row(y, "from b", hlc.HLC{})})
	layerWrite(t, a, key, store.Change{Row: g.row(z, "from a again", hlc.HLC{})})
	if rep, err := b.LayerPush(context.Background()); err != nil || len(rep.Pushed) != 1 {
		t.Fatalf("b push: %+v %v", rep, err)
	}
	if _, err := a.LayerFetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	rep, err := a.LayerPush(context.Background())
	if err != nil || len(rep.Merged) != 1 || len(rep.Pushed) != 1 {
		t.Fatalf("a push: %+v %v", rep, err)
	}
	if rows := layerRowsOf(t, a, key); len(rows) != 3 {
		t.Fatalf("merged layer has %d rows, want 3", len(rows))
	}
}

// TestLayerDeclaresFormatFeature: the first layer of a folder with a
// _meta.json adds branch_layers (and lore 0.1.19) to it in one commit on
// the store branch; a second layer does not commit again.
func TestLayerDeclaresFormatFeature(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	seed := filepath.Join(t.TempDir(), "seed")
	gitT(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	if err := os.MkdirAll(filepath.Join(seed, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, "app", rowfs.MetaFileName), []byte("{\n  \"format\": 2\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, seed, "add", ".")
	gitT(t, seed, "commit", "--quiet", "-m", "app folder")
	gitT(t, seed, "push", "--quiet", "origin", testBranch)
	b, err := OpenClone(context.Background(), remote, filepath.Join(t.TempDir(), "c"), "app", store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	g := newGen(t)
	key := branchlayer.Key{StoreBranch: testBranch, Folder: "app", RepoKey: "abc123", Branch: "feat/x"}
	res := layerWrite(t, b, key, store.Change{Row: g.row(newID(t), "x", hlc.HLC{})})
	if res.Declared == "" {
		t.Fatal("no declaration commit")
	}
	d, err := b.folder.Format(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// a 0.1.18 lore does not know the feature (Since 0.1.19) and the floor
	// is above it: either alone makes it read-only there
	if f, ok := formatgate.Lookup(formatgate.BranchLayers); !ok || f.Since != "0.1.19" || d.MinLoreVersion != "0.1.19" {
		t.Fatalf("declaration %+v, feature %+v", d, f)
	}
	if v := formatgate.Check("0.1.19", d); !v.Writable {
		t.Fatalf("0.1.19 may not write: %+v", v)
	}
	key2 := key
	key2.Branch = "feat/y"
	if res2 := layerWrite(t, b, key2, store.Change{Row: g.row(newID(t), "y", hlc.HLC{})}); res2.Declared != "" {
		t.Fatal("declared twice")
	}
	if st := gitT(t, b.Dir(), "status", "--porcelain"); st != "" {
		t.Fatalf("worktree: %q", st)
	}
}

// TestLayerDropListPurge: drop moves a layer to the trash, the listing
// shows it there, purge removes it for good.
func TestLayerDropListPurge(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b := clone(t, remote)
	g := newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "seed", hlc.HLC{})})
	syncOK(t, b)
	key := layerKey("fix/login")
	layerWrite(t, b, key, store.Change{Row: g.row(newID(t), "x", hlc.HLC{})})
	list, err := b.LayerList(context.Background(), "", true)
	if err != nil || len(list) != 1 || list[0].Layer.Key != key || list[0].Counts[memories] != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	trash, err := b.LayerTrash(context.Background(), key, branchlayer.StateDropped, "", false)
	if err != nil {
		t.Fatal(err)
	}
	list, _ = b.LayerList(context.Background(), "", true)
	if len(list) != 1 || list[0].Trash != trash {
		t.Fatalf("after drop: %+v", list)
	}
	purged, err := b.LayerPurge(context.Background(), key, time.Time{}, false)
	if err != nil || len(purged) != 1 {
		t.Fatalf("purge: %v %v", purged, err)
	}
	if list, _ = b.LayerList(context.Background(), "", true); len(list) != 0 {
		t.Fatalf("after purge: %+v", list)
	}
}

// TestLayerPushRetiresTrashedLayer (bl:F1): a layer one clone landed is
// never pushed back by another clone that still holds its old local ref;
// that clone retires its copy instead.
func TestLayerPushRetiresTrashedLayer(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	write(t, a, store.Change{Row: g.row(newID(t), "seed", hlc.HLC{})})
	syncOK(t, a)
	key := layerKey("feat/x")
	layerWrite(t, a, key, store.Change{Row: g.row(newID(t), "x", hlc.HLC{})})
	if _, err := a.LayerPush(context.Background()); err != nil {
		t.Fatal(err)
	}
	// b takes the layer as its own (a write makes the local ref)
	if _, err := b.LayerFetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	syncOK(t, b)
	l, _ := b.LayerOpen(context.Background(), key)
	if err := b.casRef(context.Background(), key.Ref(), l.Tip, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.LayerLand(context.Background(), LandRequest{Key: key, Subject: "lore: land feat/x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.LayerFetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	rep, err := b.LayerPush(context.Background())
	if err != nil || len(rep.Retired) != 1 || len(rep.Pushed) != 0 {
		t.Fatalf("push after a landing elsewhere: %+v %v", rep, err)
	}
	if out := gitT(t, b.Dir(), "ls-remote", remote, key.Ref()); out != "" {
		t.Fatalf("the landed layer is back on the remote: %s", out)
	}
	// two trash moves in one second get two names
	k2 := layerKey("feat/twice")
	for range 2 {
		layerWrite(t, a, k2, store.Change{Row: g.row(newID(t), "t", hlc.HLC{})})
		if _, err := a.LayerTrash(context.Background(), k2, branchlayer.StateDropped, "", false); err != nil {
			t.Fatal(err)
		}
	}
	if list, _ := a.LayerList(context.Background(), "", true); len(list) < 2 {
		t.Fatalf("trash list: %+v", list)
	}
}
