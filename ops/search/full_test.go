package search_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/schema"
)

// TestGlobalSearchFull (devco D-758, owner P-8): with --full (json "full")
// every hit carries the row's whole body as text, a personal-store memory
// and a repo decision alike, and the terminal prints it under the hit
// line; without it the hit holds only the snippet, so --json is unchanged.
func TestGlobalSearchFull(t *testing.T) {
	h := opstest.WithStores(t, []string{"me"})
	tail := " then parks the event in the dead letter queue for a manual replay after the ledger totals match"
	memBody := storeSearchWord + " webhooks retry five times with exponential backoff starting at two seconds," + tail
	decBody := storeSearchWord + " we chose the queue over cron because a missed tick lost events," + tail
	memID := h.SeedTo("me", schema.TableMemories, map[string]string{"body": `"` + memBody + `"`})
	decID := h.Seed(schema.TableDecisions, map[string]string{"title": `"Queue the webhooks"`, "body": `"` + decBody + `"`})
	run := func(in string) search.RunOut {
		var out search.RunOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(search.OpRun, in))), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	full := run(`{"query":"` + storeSearchWord + `","full":true}`)
	want := map[string]string{memID: memBody, decID: decBody}
	for _, x := range full.Data.Hits {
		if w, ok := want[x.ID]; ok && x.Text != w {
			t.Fatalf("--full hit %s text = %q, want the whole body %q", x.ID, x.Text, w)
		}
		delete(want, x.ID)
	}
	if len(want) != 0 {
		t.Fatalf("--full missed hits %v", want)
	}
	if txt := full.Text(); !strings.Contains(txt, "    "+memBody+"\n") || !strings.Contains(txt, "    "+decBody+"\n") {
		t.Fatalf("--full terminal text lacks the whole bodies:\n%s", txt)
	}
	plain := run(`{"query":"` + storeSearchWord + `"}`)
	for _, x := range plain.Data.Hits {
		if x.Text != "" {
			t.Fatalf("without --full hit %s carries text %q", x.ID, x.Text)
		}
	}
	if txt := plain.Text(); strings.Contains(txt, tail) {
		t.Fatalf("without --full the terminal printed the whole body:\n%s", txt)
	}
}

// TestFullText: the body when set, else the longest searched field that is
// not the title.
func TestFullText(t *testing.T) {
	row := map[string]string{"name": "deploy", "description": "how we ship the web app", "base_url": "https://x.test"}
	get := func(n string) string { return row[n] }
	if got := search.FullText(get, []string{"name", "description", "base_url"}, "name"); got != row["description"] {
		t.Fatalf("FullText = %q", got)
	}
	row["body"] = "the body"
	if got := search.FullText(get, []string{"name", "description"}, "name"); got != "the body" {
		t.Fatalf("FullText with a body = %q", got)
	}
}
