package row_test

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/row"
)

// TestShowMarksStaleFact (devco:D-953, bug hunt H-212): lore show of a row
// whose fact_evidence file changed in git after its fact_verified_at says
// so, with the command that clears it (the words lint, search and ask
// print), in text and --json; a fresh row says nothing; running the
// printed command clears it.
func TestShowMarksStaleFact(t *testing.T) {
	h := opstest.New(t)
	h.CommitOn("2026-11-01", map[string]string{"billing/refund.go": "package billing\n\nfunc Refund() {}\n", "billing/card.go": "package billing\n\nfunc Card() {}\n"})
	stale := h.ID(h.Must("memory.add", `{"body":"Refunds are capped at the order total.","meta":["fact_evidence=billing/refund.go:3","fact_verified_at=2026-11-02"]}`))
	fresh := h.ID(h.Must("memory.add", `{"body":"Card refunds go back to the original card.","meta":["fact_evidence=billing/card.go:3","fact_verified_at=2026-11-02"]}`))
	h.CommitOn("2026-11-10", map[string]string{"billing/refund.go": "package billing\n\n// no cap any more\nfunc Refund() {}\n"})

	today := time.Now().UTC().Format(time.DateOnly)
	note := "stale: evidence changed since 2026-11-02 (billing/refund.go)"
	fix := "when it still holds: lore meta set --id " + stale + " --pair fact_verified_at=" + today + "; when it does not: edit the row and its fact_evidence"
	text := opstest.Text(h.Must(row.OpShow, `{"id":"`+stale+`"}`))
	if !strings.Contains(text, "  "+note+"\n  fix: "+fix+"\n") {
		t.Fatalf("show of a stale fact misses the mark and fix:\n%s", text)
	}
	if out := showJSON(t, h, `{"id":"`+stale+`"}`); out.Stale != note || out.Reverify != fix {
		t.Fatalf("--json stale %q reverify %q", out.Stale, out.Reverify)
	}
	if text := opstest.Text(h.Must(row.OpShow, `{"id":"`+fresh+`"}`)); strings.Contains(text, "stale") {
		t.Fatalf("show of a fresh fact says stale:\n%s", text)
	}

	h.MustYes("meta.set", `{"id":"`+stale+`","pair":"fact_verified_at=`+today+`"}`)
	if out := showJSON(t, h, `{"id":"`+stale+`"}`); out.Stale != "" || out.Reverify != "" {
		t.Fatalf("the printed fix did not clear it: %+v", out)
	}
}
