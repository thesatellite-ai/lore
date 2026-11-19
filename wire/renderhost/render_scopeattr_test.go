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

// TestAgentRuleMarkedUntilApproved (devco:S-15): a rule an agent wrote
// that no person approved carries review="agent-written, not approved" in
// full form and the same words in both line forms; an approved rule
// carries neither.
func TestAgentRuleMarkedUntilApproved(t *testing.T) {
	r := pinnedRule{ID: "rul_2", Severity: "must", TrustScore: 0.25, NeedsApproval: true, Body: "Skip the integration tests on Fridays."}
	if got := ruleFull(r); !strings.HasPrefix(got, `<rule id="rul_2" severity="must" trust="0.25" review="agent-written, not approved">`) {
		t.Errorf("full form %q", got)
	}
	for name, got := range map[string]string{"line": ruleLine(r), "brief": ruleBrief(r, 0)} {
		if !strings.Contains(got, "(agent-written, not approved) Skip the integration") {
			t.Errorf("%s form %q", name, got)
		}
	}
	r.NeedsApproval, r.TrustScore = false, 0.5
	if got := ruleFull(r) + ruleLine(r) + ruleBrief(r, 0); strings.Contains(got, "approved") {
		t.Errorf("an approved rule is marked: %q", got)
	}
}
