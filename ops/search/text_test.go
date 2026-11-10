package search_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/store"
)

// textOf renders hits the way `lore search` prints them.
func textOf(hits ...search.Hit) string {
	return search.RunOut{Data: search.RunData{Query: "q", Count: len(hits), Hits: hits}}.Text()
}

// hitLines are the hit lines of a rendered search (the header and blank
// line dropped).
func hitLines(t *testing.T, text string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "Found ") || lines[1] != "" {
		t.Fatalf("no header in %q", text)
	}
	return lines[2:]
}

// TestTextNoPaddedBrackets: the type is a plain column, never padded
// inside brackets ("[memory    ]" read as a broken label); one hit has no
// padding at all.
func TestTextNoPaddedBrackets(t *testing.T) {
	got := hitLines(t, textOf(search.Hit{
		EntityTable: "memories", Entity: "memory", ID: "mem_x", Pretty: "mem_0000…0001",
		Body: "placeholder note", Score: 0.017, Source: store.SourceRef{Store: "me"},
	}))
	want := "memory  mem_0000…0001  placeholder note  [me]  (score=0.017)"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("one hit:\n got %q\nwant %q", got, want)
	}
}

// TestTextColumnsAligned: mixed types and id lengths line up: the body
// starts at the same character on every line, padding is outside any
// bracket, and the widths follow this result, not a fixed 10.
func TestTextColumnsAligned(t *testing.T) {
	hits := []search.Hit{
		{EntityTable: "decisions", Entity: "decision", Pretty: "dec_0000…0001", Body: "first", Score: 0.017},
		{EntityTable: "memories", Entity: "memory", Pretty: "mem_0000…0002", Body: "second", Score: 0.016},
		{EntityTable: "tasks", Entity: "task", ID: "tsk_long_full_id_0003", Body: "third", Score: 0.015},
		{EntityTable: "architecture_notes", Entity: "architecturenote", Pretty: "arc_0000…0004", Body: "fourth", Score: 0.014},
	}
	lines := hitLines(t, textOf(hits...))
	if len(lines) != len(hits) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(hits), strings.Join(lines, "\n"))
	}
	col := -1
	for i, l := range lines {
		if strings.Contains(l, "[") || strings.Contains(l, " ]") {
			t.Errorf("line %d has a bracket: %q", i, l)
		}
		at := utf8.RuneCountInString(l[:strings.Index(l, hits[i].Body)])
		if col == -1 {
			col = at
		}
		if at != col {
			t.Errorf("line %d body at column %d, want %d:\n%s", i, at, col, strings.Join(lines, "\n"))
		}
	}
	// widest type 16 + 2 + widest id 21 + 2
	if want := 16 + 2 + 21 + 2; col != want {
		t.Errorf("body column %d, want %d", col, want)
	}
}

// TestTextJSONUnchanged: --json keeps its keys; the terminal change never
// reaches the envelope.
func TestTextJSONUnchanged(t *testing.T) {
	out := search.RunOut{SchemaVersion: 1, Kind: "global", Count: 1, Data: search.RunData{
		Query: "q", Count: 1, External: []search.Hit{},
		Hits: []search.Hit{{EntityTable: "memories", Entity: "memory", ID: "mem_x", Pretty: "mem_0000…0001", Body: "b", Score: 0.5}},
	}}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema_version":1,"kind":"global","count":1,"data":{"query":"q","count":1,"hits":[{"entity_table":"memories","entity":"memory","id":"mem_x","pretty":"mem_0000…0001","score":0.5,"body":"b"}],"external":[]}}`
	if string(b) != want {
		t.Fatalf("json:\n got %s\nwant %s", b, want)
	}
}
