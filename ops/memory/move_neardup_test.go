package memory_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/memory"
)

// TestMoveListsNearDuplicates (devco D-854, owner org-setup item 13):
// `lore memory move` into a store that already holds a memory with the
// same first line lists the pair as the bulk move does (both ids, the read
// of both, the delete that drops one); both memories stay, nothing is
// merged, and --json carries the pair under duplicates. A memory with
// nothing like it there lists none. Break-verification: without the
// finder call the move listed nothing.
func TestMoveListsNearDuplicates(t *testing.T) {
	h := opstest.WithStores(t, []string{"me"})
	held := h.ID(h.Must(memory.OpAdd, `{"body":"Invoices round at the line, never the total.","to":"me"}`))
	copied := h.ID(h.Must(memory.OpAdd, `{"body":"invoices round at the line, never the total"}`))
	other := h.ID(h.Must(memory.OpAdd, `{"body":"The staging database resets every Sunday"}`))

	res := h.MustYes(memory.OpMove, `{"id":"`+copied+`","to":"me"}`)
	var out memory.WriteOut
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Duplicates) != 1 || out.Duplicates[0].ID != copied || out.Duplicates[0].Existing != held {
		t.Fatalf("memory move duplicates: %s", h.JSON(res))
	}
	txt := opstest.Text(res)
	for _, want := range []string{
		copied + " moved to me",
		"1 near-duplicate: moved rows that repeat a row already in me (both kept",
		copied + " ~ " + held + ": memory",
		"compare: lore show --id " + copied + " --store me && lore show --id " + held + " --store me",
		"drop one: lore memory delete --id " + held + " --yes (the folder's), or the moved one: lore memory delete --id " + copied + " --yes",
	} {
		if !strings.Contains(txt, want) {
			t.Fatalf("memory move text lacks %q:\n%s", want, txt)
		}
	}

	res = h.MustYes(memory.OpMove, `{"id":"`+other+`","to":"me"}`)
	if txt = opstest.Text(res); strings.Contains(txt, "near-duplicate") || strings.Contains(h.JSON(res), `"duplicates"`) {
		t.Fatalf("a memory with no near-duplicate is listed:\n%s", txt)
	}
}
