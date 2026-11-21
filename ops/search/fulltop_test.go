package search_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/schema"
)

// TestGlobalSearchFullTop (devco D-800, owner P-17): --full prints the
// whole text of the best hits only (search.DefaultFullTop, or --full-top
// N), each hit line holds no snippet when its whole text follows (the
// snippet repeated the body's first words), and the other hits keep their
// snippet line, with a note naming --full-top. --json carries text on the
// same top hits only. --full-top alone turns --full on.
func TestGlobalSearchFullTop(t *testing.T) {
	h := opstest.New(t)
	const word = "zephyrledger"
	tail := " reconciliation compares the bank export with the invoice table and parks every mismatch in the review queue"
	const n = 6
	bodies := map[string]string{}
	for i := range n {
		body := fmt.Sprintf("%s note %d:%s", word, i, tail)
		bodies[h.Seed(schema.TableMemories, map[string]string{"body": `"` + body + `"`})] = body
	}
	run := func(in string) search.RunOut {
		var out search.RunOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(search.OpRun, in))), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	check := func(in string, top int) {
		t.Helper()
		var out search.RunOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(search.OpRun, in))), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Data.Hits) != n {
			t.Fatalf("%s: %d hits, want %d", in, len(out.Data.Hits), n)
		}
		for i, x := range out.Data.Hits {
			if want := i < top; (x.Text != "") != want {
				t.Fatalf("%s: hit %d (%s) text = %q, want whole text only for the top %d", in, i, x.ID, x.Text, top)
			}
			if x.Text != "" && x.Text != bodies[x.ID] {
				t.Fatalf("%s: hit %s text = %q, want %q", in, x.ID, x.Text, bodies[x.ID])
			}
		}
		txt := opstest.Text(h.Must(search.OpRun, in))
		if got := strings.Count(txt, tail); got != top {
			t.Fatalf("%s: the whole text printed %d times, want %d (snippet once, body for the top only):\n%s", in, got, top, txt)
		}
		note := fmt.Sprintf("add --full-top %d", n)
		if capped := top < n; strings.Contains(txt, note) != capped {
			t.Fatalf("%s: the cut note (%q) present = %v, want %v:\n%s", in, note, !capped, capped, txt)
		}
		for _, line := range strings.Split(txt, "\n") {
			if strings.HasPrefix(line, "memory ") && strings.Contains(line, word) && strings.Count(txt, line) > 1 {
				t.Fatalf("%s: hit line repeated: %q", in, line)
			}
		}
	}
	check(`{"query":"`+word+`","full":true}`, search.DefaultFullTop)
	check(`{"query":"`+word+`","full":true,"full_top":5}`, 5)
	check(`{"query":"`+word+`","full_top":`+fmt.Sprint(n)+`}`, n)
	check(`{"query":"`+word+`","full_top":1}`, 1)
	for _, x := range run(`{"query":"` + word + `"}`).Data.Hits {
		if x.Text != "" {
			t.Fatalf("without --full hit %s carries text", x.ID)
		}
	}
}
