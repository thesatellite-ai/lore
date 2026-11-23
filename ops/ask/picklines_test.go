package ask

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
)

// TestAskAnswersWithTheNewestCopyOfARow pins devco:D-929 (bug hunt
// H-187): a row two stores hold (store adopt keeps ids) gives the text
// statement one line per copy; the corpus and the picked rows take the
// newest copy (same created_at, later updated_at), not the nearest
// store's. Two rows that only share an id (another created_at) keep the
// nearest line, as before.
func TestAskAnswersWithTheNewestCopyOfARow(t *testing.T) {
	txt := func(s string) value.Value { v, _ := value.TextOf(s); return v }
	line := func(id, place, body, created, updated string) []value.Value {
		return []value.Value{txt("row"), txt("memories"), txt(id), txt(place), txt(""), txt(body), txt(created), txt("0"), txt(""), txt(""), txt(body), txt(created), txt(updated)}
	}
	const c0, c1, u0, u1 = "2026-11-01T09:00:00.5Z", "2026-11-01T09:00:01Z", "2026-11-01T09:00:00.5Z", "2026-11-02T10:00:00Z"
	lines := [][]value.Value{
		line("mem_a", "notes/billing", "drains every ten minutes", c0, u0), // nearest, stale copy
		line("mem_a", "repo", "drains every hourly batch", c0, u1),         // the edited copy
		line("mem_b", "notes/billing", "first row", c0, u1),
		line("mem_b", "repo", "another row with the id", c1, u1), // another created_at: another row
		{txt("ref"), txt("memories"), txt("mem_a"), txt("res_x"), txt(""), txt(""), txt(""), txt("0"), txt(""), txt(""), nil, txt(""), txt("")},
	}
	got := map[string]string{}
	for i := range pickLines(lines) {
		got[cell(lines[i][colID])] = cell(lines[i][colBody])
	}
	want := map[string]string{"mem_a": "drains every hourly batch", "mem_b": "first row"}
	if len(got) != len(want) || got["mem_a"] != want["mem_a"] || got["mem_b"] != want["mem_b"] {
		t.Fatalf("picked %v, want %v", got, want)
	}
	c := corpusOf(lines)
	if r := c.rows[c.at["mem_a"]]; r.Body != "drains every hourly batch" || r.Place != "repo" {
		t.Fatalf("corpus holds %q in %s, want the edited copy in repo", r.Body, r.Place)
	}
}
