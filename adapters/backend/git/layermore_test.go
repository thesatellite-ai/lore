package git

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// TestLayerLandKindsKeepsTheRest: --kinds lands only the chosen tables;
// the other rows stay open, rebased onto the landing (the landed ones no
// longer count as the layer's), and the old tip is an ancestor so every
// copy fast-forwards. Exclude (--review drops) never lands.
func TestLayerLandKindsKeepsTheRest(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "seed", hlc.HLC{})})
	syncOK(t, b)
	key := layerKey("feat/kinds")
	keep, land, drop := newID(t), newID(t), newID(t)
	for _, id := range []ids.AnyID{keep, land, drop} {
		layerWrite(t, b, key, store.Change{Row: g.row(id, "row "+id.String(), hlc.HLC{})})
	}
	if _, err := b.LayerPush(context.Background()); err != nil {
		t.Fatal(err)
	}
	old, _ := b.LayerOpen(context.Background(), key)
	// everything is a memory here: land one, drop one, keep one open
	res, err := b.LayerLand(context.Background(), LandRequest{Key: key, Subject: "lore: land part", Exclude: []ids.AnyID{keep, drop}})
	if err != nil || len(res.Rows) != 1 || res.Rows[0].ID != land || len(res.Dropped) != 2 || res.Trash == "" {
		t.Fatalf("review-style land: %+v %v", res, err)
	}
	if _, err := b.Get(context.Background(), memories, keep); err == nil {
		t.Fatal("an excluded row landed")
	}
	if again, err := b.LayerLand(context.Background(), LandRequest{Key: key, Subject: "x"}); err != nil || again.AlreadyLanded == "" {
		t.Fatalf("land of a landed layer: %+v %v", again, err)
	}
	// --kinds: memories land, a rule stays open on a layer rebased onto the landing
	key2 := layerKey("feat/kinds2")
	mem := newID(t)
	layerWrite(t, b, key2, store.Change{Row: g.row(mem, "mem", hlc.HLC{})})
	rid, err := ids.New[ids.Policy]()
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.FromRow(row.Row{Table: "policies", ID: rid.Any(), HLC: g.clock.Now(hlc.HLC{}), Fields: map[string]value.Value{}})
	if err != nil {
		t.Fatal(err)
	}
	layerWrite(t, b, key2, store.Change{Row: rule})
	if _, err := b.LayerPush(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, _ := b.LayerOpen(context.Background(), key2)
	res2, err := b.LayerLand(context.Background(), LandRequest{Key: key2, Subject: "lore: land memories", Kinds: []row.Table{memories}})
	if err != nil || len(res2.Rows) != 1 || res2.Trash != "" || len(res2.Kept) != 1 {
		t.Fatalf("kinds land: %+v %v", res2, err)
	}
	after, _ := b.LayerOpen(context.Background(), key2)
	if after.Base != res2.Commit {
		t.Fatalf("the rest is not rebased onto the landing: base %s want %s", after.Base, res2.Commit)
	}
	if rows := layerRowsOf(t, b, key2); len(rows) != 1 || rows[rid.String()].ID != rid.Any() {
		t.Fatalf("rows left open: %v", rows)
	}
	if in, _ := b.isAncestor(context.Background(), before.Tip, after.Tip); !in {
		t.Fatal("the rebased layer does not fast-forward from the old tip")
	}
	if out := gitT(t, b.Dir(), "ls-remote", remote, key2.Ref()); !strings.HasPrefix(out, after.Tip) {
		t.Fatalf("rebased layer not pushed: %s", out)
	}
	_ = old
}

// TestLayerLandTakeSettlesAClash: a row both sides changed that the merger
// cannot merge lands with --take branch (the branch's text) or --take main.
func TestLayerLandTakeSettlesAClash(t *testing.T) {
	t.Parallel()
	for _, branchWins := range []bool{true, false} {
		remote := newRemote(t, true)
		b := clone(t, remote, WithMerger(conflictMerger{}))
		g := newGen(t)
		id := newID(t)
		write(t, b, store.Change{Row: g.row(id, "base", hlc.HLC{})})
		syncOK(t, b)
		key := layerKey("feat/take")
		cur := get(t, b, id)
		layerWrite(t, b, key, store.Change{Row: g.row(id, "branch text", cur.HLC), Expect: cur.HLC})
		write(t, b, store.Change{Row: g.row(id, "main text", cur.HLC), Expect: cur.HLC})
		syncOK(t, b)
		_, err := b.LayerLand(context.Background(), LandRequest{Key: key, Subject: "lore: land", Take: map[ids.AnyID]bool{id: branchWins}})
		if err != nil {
			t.Fatalf("take %v: %v", branchWins, err)
		}
		want := "main text"
		if branchWins {
			want = "branch text"
		}
		if got := get(t, b, id); !strings.Contains(string(got.Doc), want) {
			t.Fatalf("take %v: %s", branchWins, got.Doc)
		}
	}
}

// TestLayerStackedIntoAndRebase: a layer stacked on feat/a starts from its
// tip; landing it --into feat/a merges its rows there; a second stacked
// layer is rebased onto main when feat/a lands, and then lands alone.
func TestLayerStackedIntoAndRebase(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "seed", hlc.HLC{})})
	syncOK(t, b)
	ka := layerKey("feat/a")
	ra := newID(t)
	layerWrite(t, b, ka, store.Change{Row: g.row(ra, "a", hlc.HLC{})})
	stacked := func(branch string, id ids.AnyID) branchlayer.Key {
		k := layerKey(branch)
		if _, err := b.LayerWrite(context.Background(), LayerWriteRequest{Key: k, Op: "memory.add",
			Facts:   branchlayer.Facts{Repo: "example.com/acme/app", Parent: "feat/a"},
			Changes: []store.Change{{Row: g.row(id, branch, hlc.HLC{})}}}); err != nil {
			t.Fatal(err)
		}
		return k
	}
	rb, rc := newID(t), newID(t)
	kb := stacked("feat/b", rb)
	kc := stacked("feat/c", rc)
	lb, _ := b.LayerOpen(context.Background(), kb)
	if lb.Facts.Parent != "feat/a" {
		t.Fatalf("stacked facts: %+v", lb.Facts)
	}
	if rows := layerRowsOf(t, b, kb); len(rows) != 1 || rows[rb.String()].ID != rb {
		t.Fatalf("a stacked layer's own rows: %v", rows)
	}
	if _, err := b.LayerLandInto(context.Background(), kb, "feat/a", false); err != nil {
		t.Fatal(err)
	}
	if rows := layerRowsOf(t, b, ka); len(rows) != 2 {
		t.Fatalf("feat/a after b landed into it: %d rows", len(rows))
	}
	res, err := b.LayerLand(context.Background(), LandRequest{Key: ka, Subject: "lore: land feat/a"})
	if err != nil || len(res.Rebased) != 1 || res.Rebased[0] != "feat/c" {
		t.Fatalf("land a: %+v %v", res, err)
	}
	lc, _ := b.LayerOpen(context.Background(), kc)
	if lc.Facts.Parent != "" || lc.Base != res.Commit {
		t.Fatalf("feat/c not rebased: %+v base %s want %s", lc.Facts, lc.Base, res.Commit)
	}
	if rows := layerRowsOf(t, b, kc); len(rows) != 1 {
		t.Fatalf("feat/c after the rebase: %d rows", len(rows))
	}
	if _, err := b.LayerLand(context.Background(), LandRequest{Key: kc, Subject: "lore: land feat/c"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []ids.AnyID{ra, rb, rc} {
		get(t, b, id)
	}
}

// TestLayerRevertLanding: restore --revert's revert commit takes the
// landed rows off the store branch again and is pushed.
func TestLayerRevertLanding(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "seed", hlc.HLC{})})
	syncOK(t, b)
	key := layerKey("feat/undo")
	id := newID(t)
	layerWrite(t, b, key, store.Change{Row: g.row(id, "landed by mistake", hlc.HLC{})})
	res, err := b.LayerLand(context.Background(), LandRequest{Key: key, Subject: "lore: land feat/undo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.LayerRestore(context.Background(), key, time.Time{}, true); err != nil {
		t.Fatal(err)
	}
	rev, err := b.LayerRevertLanding(context.Background(), res.Commit, "feat/undo")
	if err != nil {
		t.Fatal(err)
	}
	if out := gitT(t, b.Dir(), "ls-remote", remote, "refs/heads/main"); !strings.HasPrefix(out, rev) {
		t.Fatalf("revert not pushed: %s", out)
	}
	if _, err := b.Get(context.Background(), memories, id); err == nil {
		t.Fatal("the landed row is still on the store branch")
	}
	if rows := layerRowsOf(t, b, key); len(rows) != 1 {
		t.Fatalf("the restored layer lost its row: %d", len(rows))
	}
}
