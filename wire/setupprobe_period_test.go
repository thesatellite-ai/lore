package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestIdentityPeriodNoPeriodFixText (teamfix n3): a person on the roster
// with no period at all got the fix text "You are marked as left", which
// is wrong: they never had a stint. The no-period result now picks its own
// fix text, naming the command that records the first stint; an ended
// stint keeps the "marked as left" text.
func TestIdentityPeriodNoPeriodFixText(t *testing.T) {
	none := periodResult(rosterLook{found: true, email: "mira@agency.example"}, "agency",
		map[string]string{onboard.VarOrg: "agency", onboard.VarEmail: "mira@agency.example"})
	if none.Status != onboard.StatusFail || none.Detail != "no period in agency yet" {
		t.Fatalf("no period: %+v", none)
	}
	v := onboard.FixVariant(none.Vars[onboard.VarFixVariant])
	if v != onboard.FixNoPeriod {
		t.Fatalf("no period: fix variant %q, want %q", v, onboard.FixNoPeriod)
	}
	txt := onboard.FixFor(onboard.CheckIdentityPeriod, onboard.StepIdentity, v).Text
	if strings.Contains(txt, "marked as left") || !strings.Contains(txt, "lore actor period add --actor") {
		t.Fatalf("no period: fix text %q", txt)
	}
	left := onboard.FixFor(onboard.CheckIdentityPeriod, onboard.StepIdentity, onboard.FixDefault).Text
	if !strings.Contains(left, "marked as left") {
		t.Fatalf("ended stint: fix text %q", left)
	}
}
