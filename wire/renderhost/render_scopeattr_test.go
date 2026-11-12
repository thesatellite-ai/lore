package renderhost

import (
	"strings"
	"testing"
)

// TestRuleOutsideItsFolderNamesIt: a folder rule printed in the root file
// (its folder has no file of its own) carries its scope in full and line
// form; a project rule carries none (bug hunt B3-18).
func TestRuleOutsideItsFolderNamesIt(t *testing.T) {
	r := pinnedRule{ID: "rul_1", Severity: "must", Body: "Booking dates are stored in UTC.", Dir: "src/booking"}
	if got := ruleFull(r); !strings.HasPrefix(got, `<rule id="rul_1" severity="must" trust="0.00" scope="dir:src/booking">`) {
		t.Errorf("full form %q", got)
	}
	if got := ruleLine(r); !strings.Contains(got, "(only in src/booking/) Booking dates") {
		t.Errorf("line form %q", got)
	}
	h := pinnedHotfix{ID: "hfx_1", Severity: "critical", Title: "t", Body: "b", Dir: "src/booking"}
	if got := hotfixFull(h); !strings.Contains(got, ` scope="dir:src/booking">`) {
		t.Errorf("hotfix full form %q", got)
	}
	r.Dir = ""
	if got := ruleFull(r); strings.Contains(got, "scope=") {
		t.Errorf("project rule %q", got)
	}
}
