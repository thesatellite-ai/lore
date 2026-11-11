package decision_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/decision"
	"github.com/thesatellite-ai/lore/wire"
)

// clientWithOrg makes the harness repo a client repo with an internal
// folder store attached as the org store "agency".
func clientWithOrg(t *testing.T) func(*wire.Options) {
	t.Helper()
	dir := t.TempDir()
	sid, err := ids.Parse[ids.Store]("sto_0e45506e64357c94a87e70ef1c6bbb01")
	if err != nil {
		t.Fatal(err)
	}
	if err := folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	url := fileuri.URL(dir, "")
	return func(o *wire.Options) {
		o.RepoIsClient = func(context.Context) (bool, error) { return true, nil }
		o.OrgStores = func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "agency", URL: url}}, nil
		}
	}
}

// refCount is how many refs decision show reports for id.
func refCount(t *testing.T, h *opstest.H, id string) int {
	t.Helper()
	var out struct {
		Refs []json.RawMessage `json:"refs"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must(decision.OpShow, `{"id":"`+id+`"}`))), &out); err != nil {
		t.Fatal(err)
	}
	return len(out.Refs)
}

// TestDecisionRefs (OV15b, RES 3.11, F15): decision add/edit --ref records
// a pointer; a pointer from a client repo row to an internal store row is
// refused E_LEAK_BLOCKED and nothing is written; re-adding the same
// pointer adds no second ref.
func TestDecisionRefs(t *testing.T) {
	h := opstest.New(t, clientWithOrg(t))
	repoMem := h.ID(h.Must("memory.add", `{"body":"repo note"}`))
	orgMem := h.ID(h.Must("memory.add", `{"body":"org note","to":"agency"}`))

	d := add(t, h, `{"title":"With ref","body":"b","ref":["`+repoMem+`","commit:abc123"]}`)
	if n := refCount(t, h, d); n != 2 {
		t.Fatalf("refs after add: %d, want 2", n)
	}
	// The same pointers again: nothing new.
	h.Must(decision.OpEdit, `{"id":"`+d+`","ref":["`+repoMem+`","commit:abc123"]}`)
	if n := refCount(t, h, d); n != 2 {
		t.Errorf("refs after re-add: %d, want 2", n)
	}
	// A client repo decision may not point into the internal org store.
	before := h.JSON(h.Must(decision.OpList, ``))
	h.Fails(decision.OpAdd, `{"title":"Leak","body":"b","ref":["`+orgMem+`"]}`, errs.LeakBlocked)
	if h.JSON(h.Must(decision.OpList, ``)) != before {
		t.Error("a refused add wrote a decision")
	}
	h.Fails(decision.OpEdit, `{"id":"`+d+`","ref":["`+orgMem+`"]}`, errs.LeakBlocked)
	if n := refCount(t, h, d); n != 2 {
		t.Errorf("refs after a refused edit: %d, want 2", n)
	}
	// An org store decision may point at the org row.
	od := add(t, h, `{"title":"Org","body":"b","to":"agency","ref":["`+orgMem+`"]}`)
	if n := refCount(t, h, od); n != 1 {
		t.Errorf("org decision refs: %d, want 1", n)
	}
}
