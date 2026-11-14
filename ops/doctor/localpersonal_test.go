package doctor

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestLocalPersonalStoreNote (H-17): doctor --all's store me line of a
// setup whose personal store is a folder on this machine says that no
// other copy exists and how to keep one; a git personal store, and every
// other store line, does not.
func TestLocalPersonalStoreNote(t *testing.T) {
	t.Parallel()
	me := targetStore + storeMe
	rs := []onboard.CheckResult{{Check: onboard.CheckStoreRead, Status: onboard.StatusOK, Target: me}}
	local := SetupReport{Name: "work", summary: setupSummary{localPersonal: true}}
	if got := areaLine(me, rs, local); !strings.Contains(got, "on this machine only") || !strings.Contains(got, "lore store set-url --name me") {
		t.Fatalf("local personal store line: %q", got)
	}
	if got := areaLine(targetStore+"acme", rs, local); strings.Contains(got, "on this machine only") {
		t.Fatalf("an org store line got the personal note: %q", got)
	}
	if got := areaLine(me, rs, SetupReport{Name: "work"}); strings.Contains(got, "on this machine only") {
		t.Fatalf("a git personal store got the local note: %q", got)
	}
}
