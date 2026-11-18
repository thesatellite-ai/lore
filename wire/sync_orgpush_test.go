package wire

import (
	"errors"
	"testing"

	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestOrgPushReportSentCountsRows (devco:D-527): a waiting org store clone
// that sync pushed reports the rows it sent, not the push's commits, so a
// push of store link commits with no note rows says "sent 0" with the
// "pushed local commit(s) with no note rows" note, as every store line of
// a sync does; store push alone counts commits (D-517).
func TestOrgPushReportSentCountsRows(t *testing.T) {
	t.Parallel()
	r := orgPushReport("client-store", opsstores.PushOut{Store: "client-store", Sent: 2, Rows: 0}, nil)
	if r.Sent != 0 || !r.Pushed || r.Offline {
		t.Fatalf("store link push: sent %d pushed %v offline %v, want sent 0 pushed true", r.Sent, r.Pushed, r.Offline)
	}
	line := storeCountsLine(r.Store, storeStatusOf(r.Sent, 0, 0, 0, r.Pushed), syncCounts{sent: r.Sent})
	if want := "store client-store: pushed (sent 0, received 0, merged 0, refused 0)"; line != want {
		t.Fatalf("line %q, want %q", line, want)
	}
	if r = orgPushReport("acme/org", opsstores.PushOut{Sent: 1, Rows: 3}, nil); r.Sent != 3 {
		t.Fatalf("rows pushed: sent %d, want the 3 row files", r.Sent)
	}
	if r = orgPushReport("acme/org", opsstores.PushOut{Sent: 1, Offline: true}, nil); r.Pushed || !r.Offline {
		t.Fatalf("offline push: pushed %v offline %v", r.Pushed, r.Offline)
	}
	if r = orgPushReport("acme/org", opsstores.PushOut{}, errors.New("refused")); r.Pushed || len(r.Problems) != 1 {
		t.Fatalf("failed push: pushed %v problems %v", r.Pushed, r.Problems)
	}
}
