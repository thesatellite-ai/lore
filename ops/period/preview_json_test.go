package period

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// TestPreviewReachesJSON: offboard and rejoin without --yes answer
// E_NEEDS_CONFIRM and write nothing; a --json caller gets only the error
// envelope (no Out, so no "applied": false), so the preview must be the
// error's message, not only its terminal question.
func TestPreviewReachesJSON(t *testing.T) {
	w := newWorld(t)
	o := w.ops()
	out, err := o.offboard(context.Background(), OffboardIn{Actor: "@mira", On: "2026-06-30"})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.NeedsConfirm || out.Applied {
		t.Fatalf("offboard without --yes: %+v %v", out, err)
	}
	for _, want := range []string{"offboard Mira Patel (@mira)", "acme-hub (dev, reviewer) ends 2026-06-30", "active → left"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("offboard preview message misses %q:\n%s", want, e.Message)
		}
	}
	if _, err := o.offboard(yes(), OffboardIn{Actor: "@mira", On: "2026-06-30"}); err != nil {
		t.Fatal(err)
	}
	rout, err := o.rejoin(context.Background(), RejoinIn{Actor: "@mira", Type: schema.PeriodTypeContractor, On: "2026-09-01"})
	if !errors.As(err, &e) || e.Code != errs.NeedsConfirm || rout.Applied {
		t.Fatalf("rejoin without --yes: %+v %v", rout, err)
	}
	for _, want := range []string{"memberships are not restored", "lore project member add --project acme-hub"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("rejoin preview message misses %q:\n%s", want, e.Message)
		}
	}
}
