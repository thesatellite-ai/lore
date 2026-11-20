package memory_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/memory"
)

// TestSearchSaysWhenCut (devco D-767): twelve memories match; memory
// search lists ten by default and printed only those ten lines, read as
// the whole answer. A cut list now ends with a note naming --limit and
// --json says "more": true; a list holding every match (--limit 12)
// says nothing more, and an empty search keeps v0.1.17's bytes.
func TestSearchSaysWhenCut(t *testing.T) {
	h := opstest.New(t)
	for i := range 12 {
		h.Must(memory.OpAdd, fmt.Sprintf(`{"body":"ledger budget note %d"}`, i))
	}
	for _, c := range []struct {
		in   string
		hits int
		more bool
	}{
		{`{"query":"ledger"}`, 10, true},
		{`{"query":"ledger","limit":11}`, 11, true},
		{`{"query":"ledger","limit":12}`, 12, false},
		{`{"query":"ledger","limit":50}`, 12, false},
	} {
		res := h.Must(memory.OpSearch, c.in)
		var out memory.SearchOut
		if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
			t.Fatal(err)
		}
		txt := opstest.Text(res)
		if len(out.Data) != c.hits || out.More != c.more || strings.Contains(txt, "more rows match than the ") != c.more {
			t.Errorf("memory search %s: %d hits more=%v, want %d more=%v; text:\n%s", c.in, len(out.Data), out.More, c.hits, c.more, txt)
		}
	}
	if got := h.JSON(h.Must(memory.OpSearch, `{"query":"nomatchword"}`)); strings.Contains(got, "more") {
		t.Errorf("an empty memory search changed its --json: %s", got)
	}
}
