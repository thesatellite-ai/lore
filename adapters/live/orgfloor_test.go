package live

import (
	"context"
	"errors"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
)

// TestLiveRefCarriesOrgFloor (devco:D-735): with the org's floor set
// (Ref.Floor, the org entry's min_lore_version), a live ref created after
// the declaration starts at that floor, so an older lore (whose check is a
// plain VersionBelow on min_lore_version) cannot write it; a ref an older
// lore made meanwhile is raised by the next write of a current lore, even
// one with nothing else to change; a lower floor never lowers the meta;
// and with no live ref and nothing to write, no ref is made.
func TestLiveRefCarriesOrgFloor(t *testing.T) {
	// the stamped 0.1.19 and 0.1.20 below predate verified epochs (0.1.57)
	t.Cleanup(formatgate.HideFeaturesFrom(formatgate.VerifiedEpochs))
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		floor string
		want  string
	}{
		{"org floor", "0.1.20", "0.1.20"},
		{"no org floor", "", MinWriterVersion},
	} {
		t.Run("new ref, "+c.name, func(t *testing.T) {
			a := newWorldClone(t)
			r := newRef(t, a, a.Remote)
			r.Floor = c.floor
			if res, err := r.Write(ctx, WriteRequest{Folder: RootFolder, BinaryVersion: "0.1.20"}); err != nil || res.Changed {
				t.Fatalf("an empty write on a repo with no live ref made one: changed=%v err=%v", res.Changed, err)
			}
			write(t, r, put("tsk_1", NewVersion(nil, row("tsk_1", "x", "low"), t0, "a")))
			if got := metaFloor(t, r); got != c.want {
				t.Fatalf("new live ref min_lore_version = %q, want %q", got, c.want)
			}
			if c.floor != "" && !VersionBelow("0.1.19", metaFloor(t, r)) {
				t.Fatal("an older lore (0.1.19) passes the new ref's floor")
			}
		})
	}
	t.Run("existing ref raised, never lowered", func(t *testing.T) {
		a := newWorldClone(t)
		r := newRef(t, a, a.Remote)
		write(t, r, put("tsk_1", NewVersion(nil, row("tsk_1", "x", "low"), t0, "a"))) // made before the declaration
		if got := metaFloor(t, r); got != MinWriterVersion {
			t.Fatalf("before: %q", got)
		}
		r.Floor = "0.1.20"
		if res, err := r.Write(ctx, WriteRequest{Folder: RootFolder, BinaryVersion: "0.1.20"}); err != nil || !res.Changed {
			t.Fatalf("a current lore's write did not raise the old ref: changed=%v err=%v", res.Changed, err)
		}
		if got := metaFloor(t, r); got != "0.1.20" {
			t.Fatalf("raised floor = %q", got)
		}
		if err := r.CheckFloor(ctx, "0.1.19"); !errors.Is(err, ErrBelowFloor) {
			t.Fatalf("0.1.19 may still write the raised ref: %v", err)
		}
		r.Floor = "0.1.19"
		if res, err := r.Write(ctx, WriteRequest{Folder: RootFolder, BinaryVersion: "0.1.20"}); err != nil || res.Changed {
			t.Fatalf("a lower floor changed the meta: changed=%v err=%v", res.Changed, err)
		}
		if got := metaFloor(t, r); got != "0.1.20" {
			t.Fatalf("lowered to %q", got)
		}
	})
}

// metaFloor is the local live ref's _live/meta.json min_lore_version.
func metaFloor(t *testing.T, r *Ref) string {
	t.Helper()
	_, tree, err := r.LocalTree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return decodeMeta(t, tree[MetaPath]).MinLoreVersion
}

// newWorldClone is a clone "a" of a fresh test world (a bare remote).
func newWorldClone(t *testing.T) *gitremotetest.Clone {
	t.Helper()
	return gitremotetest.NewWorld(t).Clone("a")
}
