package live

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
)

// F-TOMB-1 (DQ4, LORE_TEAM_SPEC side effect 11): a purged id's tombstone
// stays in a merged tree, so it can land as a file delete plus a
// _purged.json id; a content file of the same id still goes (§6.6).
func TestMergeTreesKeepsPurgedTombstone(t *testing.T) {
	purged, _ := encodePurged(map[string]bool{"tsk_tomb": true, "tsk_gone": true})
	tomb, _ := Tombstone("tsk_tomb", Block{LiveSince: t0}).Encode()
	gone, _ := File{Body: obj(map[string]string{"id": "tsk_gone", "title": "x"})}.Encode()
	ours := Tree{EpochPath: encodeEpoch(1), "_root/tasks/tsk_tomb.json": tomb, "_root/tasks/tsk_gone.json": gone}
	theirs := Tree{EpochPath: encodeEpoch(1), PurgedPath: purged, "_root/tasks/tsk_tomb.json": tomb}
	out, _, err := MergeTrees(Tree{}, ours, theirs, strategies)
	if err != nil {
		t.Fatal(err)
	}
	b, has := out["_root/tasks/tsk_tomb.json"]
	if !has {
		t.Fatal("a purged id's tombstone must stay in live until it lands")
	}
	if f, err := DecodeFile(b); err != nil || !f.Deleted() {
		t.Fatalf("the kept file must be the tombstone: %v %+v", err, f)
	}
	if _, has := out["_root/tasks/tsk_gone.json"]; has {
		t.Fatal("a purged id's content must never come back")
	}
}

// F-TOMB-1: ListPurged (the `lore trash purge` path) lists the id and
// keeps its tombstone; compaction keeps the tombstone too.
func TestListPurgedKeepsTheTombstone(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	r := newRef(t, a, a.Remote)
	write(t, r, put("tsk_purged", NewVersion(nil, row("tsk_purged", "gone soon", "low"), t0, "act_a")))
	write(t, r, put("tsk_purged", Tombstone("tsk_purged", Block{LiveSince: t0})))
	_, err := r.ListPurged(ctx, []string{"tsk_purged"}, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	_, tree, err := r.LocalTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := tree.Purged(); !p["tsk_purged"] {
		t.Fatal("ListPurged must list the id")
	}
	assertTombstone(t, tree, "tsk_purged")
	mustPush(t, r)
	if _, err = r.Compact(ctx, false); err != nil {
		t.Fatal(err)
	}
	_, tree, err = r.LocalTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertTombstone(t, tree, "tsk_purged")
}

func assertTombstone(t *testing.T, tree Tree, id string) {
	t.Helper()
	b, ok := tree[RowPath(RootFolder, "tasks", id)]
	if !ok {
		t.Fatalf("%s: the tombstone must stay in live; tree %v", id, keys(tree))
	}
	if f, err := DecodeFile(b); err != nil || !f.Deleted() {
		t.Fatalf("%s: want a tombstone: %v %+v", id, err, f)
	}
}

func keys(t Tree) []string {
	out := make([]string, 0, len(t))
	for k := range t {
		out = append(out, k)
	}
	return out
}
