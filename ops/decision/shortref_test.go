package decision_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/decision"
)

// TestShortRefAndStatus: a decision is found by the number its title opens
// with (D5), never by a longer one (D51), and decision list --status keeps
// only the statuses named (devco D-449).
func TestShortRefAndStatus(t *testing.T) {
	h := opstest.New(t)
	d5 := add(t, h, `{"title":"D5: Which repos are client repos","body":"client wins","status":"proposed"}`)
	add(t, h, `{"title":"D51 unrelated","body":"b"}`)
	add(t, h, `{"title":"D7 twice","body":"b"}`)
	add(t, h, `{"title":"d7: twice again","body":"b","status":"proposed"}`)
	if got := show(t, h, "D5").ID; got != d5 {
		t.Fatalf("show D5 = %s, want %s", got, d5)
	}
	if got := show(t, h, "d5").ID; got != d5 {
		t.Fatalf("show d5 = %s, want %s", got, d5)
	}
	if msg := h.Fails(decision.OpShow, `{"id":"D7"}`, errs.AmbiguousID); !strings.Contains(msg, "dec_") {
		t.Fatalf("ambiguous D7 names no ids: %s", msg)
	}
	h.Fails(decision.OpShow, `{"id":"D9"}`, errs.NotFound)
	h.Must(decision.OpEdit, `{"id":"D5","status":"accepted"}`)
	if got := show(t, h, d5).Status; got != "accepted" {
		t.Fatalf("edit D5: status %s", got)
	}
	out := opstest.Text(h.Must(decision.OpList, `{"status":["proposed"]}`))
	if !strings.Contains(out, "d7: twice again") || strings.Contains(out, "D51") || strings.Contains(out, "D5:") {
		t.Fatalf("list --status proposed:\n%s", out)
	}
	h.Fails(decision.OpList, `{"status":["waiting"]}`, errs.InvalidInput)
}
