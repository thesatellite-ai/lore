package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRowLookupAnswerNoStoreD625: an id looked up outside a repo on a
// machine with no store folder at all (no setup, no org entry) leaves the
// folder's own E_NOT_A_STORE refusal to answer (nil here), so `lore show
// <id>` in a bare home keeps exit 19 and its hint; once store folders were
// searched, an id in none of them is E_NOT_FOUND naming where lore looked
// (devco:D-589).
func TestRowLookupAnswerNoStoreD625(t *testing.T) {
	const id = "mem_01a1b4a47d1e75c1aa1a0b15f1643889"
	if !storedRowID(id) {
		t.Fatalf("%s is not a stored row id", id)
	}
	if err := (&rowLookup{id: id}).answer(""); err != nil {
		t.Fatalf("no store folder looked in: answer = %v, want nil (the E_NOT_A_STORE refusal answers)", err)
	}
	err := (&rowLookup{id: id, looked: []string{"acme/sync"}}).answer("")
	if !errs.Has(err, errs.NotFound) {
		t.Fatalf("searched acme/sync: answer = %v, want E_NOT_FOUND", err)
	}
	if got := err.Error(); !strings.Contains(got, "looked in acme/sync") {
		t.Fatalf("answer %q does not say where lore looked", got)
	}
}
