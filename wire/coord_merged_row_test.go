package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
)

// TestUnmergedRowMatchesTheDocument: the merged check's quick look on the
// row says what unmerged says of the row's whole document (docOfRow), for a
// done task with and without merged_at, an open one, and a done one whose
// merged_at is empty.
func TestUnmergedRowMatchesTheDocument(t *testing.T) {
	t.Parallel()
	const head = `{"_v":2,"_table":"tasks","id":"tsk_01a1d66426e8785789706d23b90e8c48","_hlc":"2026-11-13T19:00:00.000Z/0000/","created_at":"2026-11-13T19:00:00Z","updated_at":"2026-11-13T19:00:00Z","title":"probe"`
	cases := map[string]string{
		"done, not merged": head + `,"status":"done"}`,
		"done and merged":  head + `,"status":"done","merged_at":"2026-11-13T20:00:00Z"}`,
		"open":             head + `,"status":"todo"}`,
		"done, empty":      head + `,"status":"done","merged_at":""}`,
		"no status":        head + `}`,
	}
	for name, doc := range cases {
		r, err := row.Decode([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		d, err := docOfRow(r)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, want := unmergedRow(r), unmerged(d); got != want {
			t.Errorf("%s: unmergedRow %v, unmerged(docOfRow) %v", name, got, want)
		}
		if got, want := unmergedRow(r), name == "done, not merged" || name == "done, empty"; got != want {
			t.Errorf("%s: unmergedRow %v, want %v", name, got, want)
		}
	}
}
