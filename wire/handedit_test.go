package wire

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestErrorLine: a refusal points at the line it is about.
func TestErrorLine(t *testing.T) {
	t.Parallel()
	doc := []byte("{\n  \"_hlc\": \"x\",\n  \"status\": \"someday\",\n  \"title\": \"t\"\n}\n")
	if got := errorLine(doc, errs.New(errs.ResourceInvalid, "tasks: field status: want one of open, done")); got != 3 {
		t.Errorf("field line = %d, want 3", got)
	}
	bad := []byte("{\n  \"title\": \"t\",\n  oops\n}\n")
	if got := errorLine(bad, errors.New("not JSON")); got != 3 {
		t.Errorf("syntax line = %d, want 3", got)
	}
	marked := []byte("{\n<<<<<<< ours\n  \"title\": \"a\"\n=======\n")
	if got := errorLine(marked, errors.New("markers")); got != 2 {
		t.Errorf("marker line = %d, want 2", got)
	}
	if got := errorLine(doc, errors.New("something else")); got != 1 {
		t.Errorf("no field named: line %d, want 1", got)
	}
}

// TestJSONKeys lists keys at every depth, never string values.
func TestJSONKeys(t *testing.T) {
	t.Parallel()
	got := jsonKeys([]byte(`{"a": "b", "c": {"d": ["e", {"f": 1}]}, "g": true}`))
	if want := []string{"a", "c", "d", "f", "g"}; !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

// TestPairMoves: a deleted file and a new file with the same id in another
// folder are one move; the rest stay as they are.
func TestPairMoves(t *testing.T) {
	t.Parallel()
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	other, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	rows := []gitbackend.HandChange{
		{Folder: "a", Table: "memories", ID: id.Any(), Kind: gitbackend.HandDeleted},
		{Folder: "b", Table: "memories", ID: id.Any(), Kind: gitbackend.HandAdded},
		{Folder: "a", Table: "memories", ID: other.Any(), Kind: gitbackend.HandModified},
	}
	rest, moves := pairMoves(rows)
	if len(moves) != 1 || moves[0].from.Folder != "a" || moves[0].to.Folder != "b" || len(rest) != 1 || rest[0].ID != other.Any() {
		t.Fatalf("rest %+v moves %+v", rest, moves)
	}
}

// TestDeferredFetch marks a context and leaves others alone.
func TestDeferredFetch(t *testing.T) {
	t.Parallel()
	if fetchDeferred(context.Background()) || !fetchDeferred(withDeferredFetch(context.Background())) {
		t.Fatal("withDeferredFetch / fetchDeferred disagree")
	}
}

// TestHandEditContextNeverFetches (I-1): a store read under the pick-up's
// context neither fetches nor merges (the hand edit is committed first);
// the same read under the command's context syncs as before.
func TestHandEditContextNeverFetches(t *testing.T) {
	t.Parallel()
	f := &fakePushBackend{caps: store.Capabilities{Remote: true}}
	b := &storePush{inner: f, name: "northwind", res: pushRes(t), notes: io.Discard}
	if _, _, err := b.Read(handEditContext(background()), ""); err != nil || f.syncs != 0 {
		t.Fatalf("a read inside the pick-up synced the store: syncs=%d err=%v", f.syncs, err)
	}
	if _, err := b.Get(handEditContext(background()), "tasks", ids.AnyID{}); !errors.Is(err, store.ErrNotFound) || f.syncs != 0 {
		t.Fatalf("a get inside the pick-up synced the store: syncs=%d err=%v", f.syncs, err)
	}
	if _, _, err := b.Read(background(), ""); err != nil || f.syncs != 1 {
		t.Fatalf("the read after the pick-up must sync once: syncs=%d err=%v", f.syncs, err)
	}
}
