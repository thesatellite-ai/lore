package search

import (
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

// TestSearchTakesSeveralBareWords (devco:T-2): `lore search a b` searches
// "a b", not E_INVALID_INPUT about a stray "b".
func TestSearchTakesSeveralBareWords(t *testing.T) {
	t.Parallel()
	for _, e := range Ops(nil) {
		if e.Spec().Name != OpRun {
			continue
		}
		in, err := e.Decode(op.ArgsDecoder{Args: []string{"Zephyrine", "idempotency"}})
		if err != nil {
			t.Fatalf("two bare words: %v", err)
		}
		for _, tv := range in.Texts() {
			if tv.Field == "query" {
				if tv.Text != "Zephyrine idempotency" {
					t.Fatalf("query = %q", tv.Text)
				}
				return
			}
		}
		t.Fatal("no query decoded")
	}
	t.Fatal("search.run not found")
}
