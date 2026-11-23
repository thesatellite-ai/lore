package lint_test

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestStaleFactReportedUntilReverified (devco:D-941, bug hunt H-190): a
// memory whose fact_evidence file changed in git after its
// fact_verified_at is reported as stale_fact with the changed file, its
// commit and the command that re-verifies it; a fact whose file did not
// change, and one never verified, are not; the printed fix clears it. The
// two keys register with their own types on first use (a list, a date),
// not as text from their first value.
func TestStaleFactReportedUntilReverified(t *testing.T) {
	h := opstest.New(t)
	h.CommitOn("2026-11-01", map[string]string{"billing/refund.go": "package billing\n\nfunc Refund() {}\n", "billing/card.go": "package billing\n\nfunc Card() {}\n"})
	stale := h.ID(h.Must("memory.add", `{"body":"Refunds are capped at the order total.","meta":["fact_evidence=billing/refund.go:3","fact_verified_at=2026-11-02"]}`))
	fresh := h.ID(h.Must("memory.add", `{"body":"Card refunds go back to the original card.","meta":["fact_evidence=billing/card.go:3","fact_verified_at=2026-11-02"]}`))
	unverified := h.ID(h.Must("memory.add", `{"body":"Refunds log the reason.","meta":["fact_evidence=billing/refund.go:1"]}`))
	sha := h.CommitOn("2026-11-10", map[string]string{"billing/refund.go": "package billing\n\n// no cap any more\nfunc Refund() {}\n"})

	out := opstest.Text(h.Must("lint.run", `{"code":["stale_fact"]}`))
	today := time.Now().UTC().Format(time.DateOnly)
	for _, want := range []string{"stale_fact " + stale, "changed after 2026-11-02", "billing/refund.go (in " + sha + " on 2026-11-10)",
		"lore meta set --id " + stale + " --pair fact_verified_at=" + today, "1 finding(s)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("lint misses %q:\n%s", want, out)
		}
	}
	for _, not := range []string{fresh, unverified} {
		if strings.Contains(out, not) {
			t.Fatalf("lint reports %s, whose evidence did not change after it was verified:\n%s", not, out)
		}
	}

	h.MustYes("meta.set", `{"id":"`+stale+`","pair":"fact_verified_at=`+today+`"}`)
	if out := opstest.Text(h.Must("lint.run", `{"code":["stale_fact"]}`)); strings.Contains(out, stale) {
		t.Fatalf("the printed fix did not clear the finding:\n%s", out)
	}

	keys := h.JSON(h.Must("meta.keys", ""))
	for _, want := range []string{`"fact_evidence"`, `"list"`, `"fact_verified_at"`, `"date"`} {
		if !strings.Contains(keys, want) {
			t.Fatalf("meta keys misses %s:\n%s", want, keys)
		}
	}
}

// TestStaleFactFixClearsWhenTheClockIsBehind: a commit dated after today
// (a skewed clock, a rebase) would stay stale under a fix naming today, so
// the printed day is the newest change's; running it clears the finding.
func TestStaleFactFixClearsWhenTheClockIsBehind(t *testing.T) {
	h := opstest.New(t, opstest.Fixed(time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)))
	h.CommitOn("2026-11-01", map[string]string{"svc/a.go": "package svc\n"})
	id := h.ID(h.Must("memory.add", `{"body":"The service starts on boot.","meta":["fact_evidence=svc/a.go:1","fact_verified_at=2026-11-02"]}`))
	h.CommitOn("2026-11-10", map[string]string{"svc/a.go": "package svc\n\n// changed\n"})
	out := opstest.Text(h.Must("lint.run", `{"code":["stale_fact"]}`))
	if !strings.Contains(out, "--pair fact_verified_at=2026-11-10") {
		t.Fatalf("the fix does not name the change's day:\n%s", out)
	}
	h.MustYes("meta.set", `{"id":"`+id+`","pair":"fact_verified_at=2026-11-10"}`)
	if out := opstest.Text(h.Must("lint.run", `{"code":["stale_fact"]}`)); strings.Contains(out, id) {
		t.Fatalf("the printed fix did not clear the finding:\n%s", out)
	}
}
