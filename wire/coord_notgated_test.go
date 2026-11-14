package wire

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestCoordNotGatedIsRefused (devco:L-3): approving a task nobody asked a
// gate for is a refusal by the data's state naming the task, with the
// next step `lore gate request --id <task>`; it was E_INTERNAL.
func TestCoordNotGatedIsRefused(t *testing.T) {
	t.Parallel()
	id := "tsk_01a1da0d50ed7c1fb53cf6a40985aa2f"
	err := coordErr(fmt.Errorf("%w: %s (request one with `lore gate request --id %s`)", coord.ErrNotGated, id, id))
	env := errs.EnvelopeOf(err)
	if env.Error.Code != errs.Refused || !strings.Contains(env.Error.Message, id+" has no gate to approve") || !strings.Contains(env.Error.Hint, "lore gate request --id "+id) {
		t.Fatalf("not gated: %+v", env.Error)
	}
}
