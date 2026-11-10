package host

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// ltClientActor is the handle-less actor row `lore init` commits in a
// client repo (id, display name, kind; no handle, no stable key).
const ltClientActor = "act_01a1a6841fc0784d92a62b0acf148b2a"

// commitActorFile writes r as the actor's row file and commits it.
func commitActorFile(t *testing.T, dir string, r store.Row, msg string) {
	t.Helper()
	rel := mainDataPath(live.RootFolder, schema.TableActors+"/"+ltClientActor+rowFileExt)
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, r.Doc, 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", msg)
}

// readActor is the actor as a full Read (`actor list`) sees it.
func readActor(t *testing.T, f ltFixture) store.Row {
	t.Helper()
	rows, _, err := f.b.Read(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []store.Row
	for _, r := range rows {
		if r.Table == row.Table(schema.TableActors) && r.ID.String() == ltClientActor {
			got = append(got, r)
		}
	}
	if len(got) != 1 {
		t.Fatalf("actor list holds the actor %d times, want once", len(got))
	}
	return got[0]
}

// TestLiveBackend_LandedActorCopy (later 63, T1 on cmd/lore): clone A
// holds the committed handle-less actor row; clone B's newer copy landed on
// main (A fetched, did not pull). A's actor show (Get) and actor list
// (Read) see B's copy, once; an edit expecting B's copy writes over the
// branch file (no E_STALE), an edit from a version never read is stale;
// a local work-tree edit A has not committed wins over the landed copy,
// even when it is older.
func TestLiveBackend_LandedActorCopy(t *testing.T) {
	aV1 := ltRow(t, schema.TableActors, ltClientActor, 1, "A's copy", false)
	bV2 := ltRow(t, schema.TableActors, ltClientActor, 2, "B's landed copy", false)

	// setup: HEAD holds v1, origin/main holds v2, the work tree (branch
	// data) holds what HEAD holds unless local says otherwise.
	setup := func(t *testing.T, local store.Row) ltFixture {
		f := newLiveFixture(t)
		dir := f.b.top
		commitActorFile(t, dir, bV2, "B lands")
		gitT(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
		gitT(t, dir, "reset", "-q", "--hard", "HEAD~1")
		commitActorFile(t, dir, aV1, "A's committed row")
		if err := f.inner.Write(f.ctx, []store.Change{{Row: local}}); err != nil {
			t.Fatal(err)
		}
		return f
	}

	t.Run("landed copy is read and edited", func(t *testing.T) {
		f := setup(t, aV1)
		got, err := f.b.Get(f.ctx, row.Table(schema.TableActors), bV2.ID)
		if err != nil || got.HLC.Compare(bV2.HLC) != 0 {
			t.Fatalf("actor show reads A's copy, want B's landed one: %v", err)
		}
		if l := readActor(t, f); l.HLC.Compare(bV2.HLC) != 0 {
			t.Fatal("actor list reads A's copy, want B's landed one")
		}
		// A version nobody read stays stale (A's own committed copy is the
		// branch file, so an edit from it is an ordinary concurrent edit
		// the merge driver reconciles field by field, not a stale one).
		stale := ltRow(t, schema.TableActors, ltClientActor, 3, "from a version never read", false)
		if err := f.b.Write(f.ctx, []store.Change{{Row: stale, Expect: ltRow(t, schema.TableActors, ltClientActor, 9, "x", false).HLC}}); !errors.Is(err, store.ErrStale) {
			t.Fatalf("an edit expecting a version never read: %v, want E_STALE", err)
		}
		edit := ltRow(t, schema.TableActors, ltClientActor, 4, "edited over B's copy", false)
		if err := f.b.Write(f.ctx, []store.Change{{Row: edit, Expect: bV2.HLC}}); err != nil {
			t.Fatalf("an edit of the landed copy: %v, want written (no E_STALE)", err)
		}
		if br, err := f.inner.Get(f.ctx, row.Table(schema.TableActors), edit.ID); err != nil || br.HLC.Compare(edit.HLC) != 0 {
			t.Fatalf("the edit writes over the branch file: %v", err)
		}
	})

	for _, c := range []struct {
		name string
		sec  int
	}{{"newer local edit wins", 5}, {"older local edit still wins", 0}} {
		t.Run(c.name, func(t *testing.T) {
			local := ltRow(t, schema.TableActors, ltClientActor, c.sec, "A's uncommitted edit", false)
			f := setup(t, local)
			got, err := f.b.Get(f.ctx, row.Table(schema.TableActors), local.ID)
			if err != nil || got.HLC.Compare(local.HLC) != 0 {
				t.Fatalf("actor show: %v, want A's local work-tree edit", err)
			}
			if l := readActor(t, f); l.HLC.Compare(local.HLC) != 0 {
				t.Fatal("actor list: want A's local work-tree edit")
			}
		})
	}
}
