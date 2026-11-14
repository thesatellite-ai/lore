package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestRefusedMoveLeavesNoActorRow (batch leftover-3): the caller's first
// write into a store is a move the engine refuses (the row was in that
// store before and left it). The actor row is staged with the copy, so
// the refusal writes nothing there, not even the caller's actor row; a
// move that goes through writes both.
func TestRefusedMoveLeavesNoActorRow(t *testing.T) {
	t.Parallel()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	repo := gitRepo(t)
	a := openWith(t, repo, Options{
		OrgStores: func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "agency", URL: orgURL, Folder: "acme"}}, nil
		},
		RepoIsClient: func(context.Context) (bool, error) { return false, nil },
	})
	withProject(t, a)
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	// the row's id has a tombstone in agency: written by the engine
	// directly, so no actor row of the caller is in agency yet
	id := idOf(t, mustRun(t, a, opsmemory.OpAdd, `{"body":"left the agency once"}`))
	mid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	_, cur, err := eng.Get(background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableMemories, ID: mid})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eng.CreateWithID(background(), "agency", schema.TableMemories, mid, cur.Fields); err != nil {
		t.Fatal(err)
	}
	if _, err = eng.Delete(background(), engine.Ref{Store: "agency", Table: schema.TableMemories, ID: mid}); err != nil {
		t.Fatal(err)
	}
	actors := func() int {
		t.Helper()
		rows, lerr := a.List(background(), "agency", schema.TableActors)
		if lerr != nil {
			t.Fatal(lerr)
		}
		n := 0
		for _, r := range rows {
			if !r.Deleted {
				n++
			}
		}
		return n
	}
	if n := actors(); n != 0 {
		t.Fatalf("agency holds %d actor rows before the move", n)
	}
	if _, err = run(t, a, opsmemory.OpMove, `{"id":"`+id+`","to":"agency"}`, true); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("move back into a store the row left: %v, want E_UNSUPPORTED", err)
	}
	if n := actors(); n != 0 {
		t.Fatalf("the refused move left %d actor row(s) in agency", n)
	}

	// a move that goes through writes the actor row with the copy
	other := idOf(t, mustRun(t, a, opsmemory.OpAdd, `{"body":"goes to the agency"}`))
	if _, err = run(t, a, opsmemory.OpMove, `{"id":"`+other+`","to":"agency"}`, true); err != nil {
		t.Fatalf("move into agency: %v", err)
	}
	if n := actors(); n != 1 {
		t.Fatalf("after a move into agency it holds %d actor rows, want the caller's", n)
	}
	oid, err := ids.ParseAny(other)
	if err != nil {
		t.Fatal(err)
	}
	if src, got, gerr := eng.Get(background(), engine.Ref{Table: schema.TableMemories, ID: oid}); gerr != nil || got.Deleted || src != "agency" {
		t.Fatalf("the moved row: %v %s %+v", gerr, src, got)
	}
}
