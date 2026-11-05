package contextfit

import (
	"strings"
	"testing"
	"time"
)

// TestFitPins (FA-CTX-6, 6.4/6.6; fa-ctx later:46): a full pin is in full
// even when the budget cannot hold it (the file goes over, like a critical
// hotfix); a line pin stays a line even with room to spare (reason
// "line form: pinned") and even when its full form is shorter; full pins
// rank first, oldest pin first, ahead of activation always.
func TestFitPins(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	big := Entry{ID: "big", Full: strings.Repeat("x", 500), Line: "l", Pin: FormFull, PinnedAt: t0}
	r := Fit(Plan{Budget: 100, Fixed: 50, Must: []Entry{big}})
	if r.Forms[0] != FormFull || r.Reasons[0] != ReasonNone || !r.Over {
		t.Fatalf("full pin over budget: %+v", r)
	}
	small := Entry{ID: "small", Full: "f", Line: "a longer line form", Pin: FormLine}
	r = Fit(Plan{Budget: 0, Must: []Entry{small}})
	if r.Forms[0] != FormLine || r.Reasons[0] != ReasonPinned {
		t.Fatalf("line pin with room: %+v", r)
	}
	es := []Entry{
		{ID: "always", Always: true, Updated: t0.Add(time.Hour)},
		{ID: "pin-late", Pin: FormFull, PinnedAt: t0.Add(2 * time.Hour)},
		{ID: "line-pin", Pin: FormLine, Always: true},
		{ID: "pin-early", Pin: FormFull, PinnedAt: t0},
	}
	Rank(es)
	got := ""
	for _, e := range es {
		got += e.ID + " "
	}
	if got != "pin-early pin-late always line-pin " {
		t.Fatalf("rank %q", got)
	}
}
