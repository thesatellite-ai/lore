package team

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestTeamMissHintsSyncNotAdd (devco:T-6): a team this clone does not have
// yet is most likely one a teammate made since the last sync; the answer
// names lore sync and never team add (which would make a second team).
func TestTeamMissHintsSyncNotAdd(t *testing.T) {
	t.Parallel()
	var e *errs.Error
	if !errors.As(teamMissErr("web", "acme org/"), &e) || e.Code != errs.NotFound {
		t.Fatalf("not E_NOT_FOUND: %v", e)
	}
	if !strings.Contains(e.Hint, "lore sync") || strings.Contains(e.Hint, "team add") {
		t.Errorf("hint = %q", e.Hint)
	}
}
