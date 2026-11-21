package wire

import (
	"encoding/json"
	"strings"
	"testing"

	opsask "github.com/thesatellite-ai/lore/ops/ask"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestStaleFactsAreHistoryInEveryRecall (devco D-820, owner): a memory
// superseded by a newer one (memory add --supersedes) or past its
// valid_until (memory invalidate) is never handed out as the answer: lore
// search lists it after the current hit and marks it, lore ask puts the
// current row first and marks the old one as history.
func TestStaleFactsAreHistoryInEveryRecall(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	a := openWith(t, gitRepo(t), Options{})
	withProject(t, a)
	add := func(in string) string {
		t.Helper()
		out, err := run(t, a, opsmemory.OpAdd, in, true)
		if err != nil {
			t.Fatal(err)
		}
		return idOf(t, out)
	}
	// the old row is short and names the gym in its first words, so on
	// words alone it outscores the new one
	old := add(`{"body":"gym: ironworks gym"}`)
	cur := add(`{"body":"Since the move in spring I train at peakfit, which is the gym two streets from the new flat, mostly in the mornings before work.","supersedes":"` + old + `"}`)
	ins := add(`{"body":"My car insurance: harbourline."}`)
	if _, err := run(t, a, opsmemory.OpInvalidate, `{"id":"`+ins+`"}`, true); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, a, opssearch.OpRun, `{"query":"gym"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var s opssearch.RunOut
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Data.Hits) != 2 || s.Data.Hits[0].ID != cur || s.Data.Hits[0].History != "" || s.Data.Hits[1].History != "history: superseded by "+cur {
		t.Errorf("search gym: want the current %s first and %s marked as history:\n%s", cur, old, out)
	}

	ask := func(q string) opsask.Out {
		t.Helper()
		out, err := run(t, a, opsask.OpRun, `{"query":"`+q+`"}`, false)
		if err != nil {
			t.Fatal(err)
		}
		var o opsask.Out
		if err := json.Unmarshal([]byte(out), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	o := ask("what is my gym now")
	if len(o.Rows) == 0 || o.Rows[0].ID != cur || o.Rows[0].History != "" {
		t.Fatalf("ask gym: rows %+v, want %s first and current", o.Rows, cur)
	}
	for _, r := range o.Rows {
		if r.ID == old && r.History != "history: superseded by "+cur {
			t.Errorf("ask gym: the superseded row is not marked: %+v", r)
		}
	}
	o = ask("who is my car insurance with")
	if len(o.Rows) != 1 || o.Rows[0].ID != ins || !strings.HasPrefix(o.Rows[0].History, "history: valid until ") {
		t.Errorf("ask car insurance: the invalidated row is not marked as history: %+v", o.Rows)
	}
}
