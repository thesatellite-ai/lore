package wire

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
)

// TestCoordOutsideRepoSaysWhereItLives (devco D-508, owner 2026-11-17):
// mission status and the inbox stay repo-only; from a folder
// that is no repo and no store clone they refuse naming where missions
// and the inbox live and to cd into the code repo, never "name a
// --store" (--store does not apply to them).
func TestCoordOutsideRepoSaysWhereItLives(t *testing.T) {
	dir := t.TempDir()
	a := openWith(t, dir, Options{})
	for _, c := range []struct{ name, in string }{
		{opscoord.OpInbox, `{}`},
		{opscoord.OpMissionStatus, `{"id":"msn_00000000000000000000000000000000"}`},
	} { // task next ranks an org store from here too: TestTaskNextOutsideNamesStoreAndSetupD683
		_, err := run(t, a, c.name, c.in, false)
		if err == nil {
			t.Fatalf("%s outside a repo: want a refusal", c.name)
		}
		var le *errs.Error
		if !errors.As(err, &le) || le.Code != errs.Unsupported {
			t.Errorf("%s: want E_UNSUPPORTED, got %v", c.name, err)
			continue
		}
		if !strings.Contains(le.Message, "live in each code repo") || !strings.Contains(le.Message, "is not inside a code repo") {
			t.Errorf("%s: message does not say where it lives: %q", c.name, le.Message)
		}
		if !strings.Contains(le.Hint, "cd into the code repo") || !strings.Contains(le.Hint, "--store does not apply") || strings.Contains(le.Hint, "--to <org>") {
			t.Errorf("%s: hint must say cd into the repo and that --store does not apply: %q", c.name, le.Hint)
		}
	}
}
