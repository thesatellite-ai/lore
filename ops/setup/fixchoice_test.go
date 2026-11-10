package setup

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestPickCommand: a fix with choices runs only the choice named by
// --choice; with none named it refuses and lists them; a plain fix takes
// no --choice.
func TestPickCommand(t *testing.T) {
	linked := onboard.FixFor(onboard.CheckToolsSkillVersion, onboard.StepTools, onboard.FixLinked)
	plain := onboard.FixFor(onboard.CheckToolsSkillVersion, onboard.StepTools, onboard.FixDefault)
	id := onboard.CheckToolsSkillVersion
	cases := []struct {
		name    string
		fix     onboard.Fix
		choice  string
		want    string // the argv template, space-joined; "" = refused
		refused string // a substring of the refusal
	}{
		{"replace", linked, onboard.ChoiceLinkReplace, "lore skill install --tool {tool} --link replace", ""},
		{"update", linked, onboard.ChoiceLinkUpdate, "lore skill install --tool {tool} --link update", ""},
		{"no choice named", linked, "", "", "--choice replace or --choice update"},
		{"unknown choice", linked, "example", "", `no choice "example"`},
		{"plain fix", plain, "", "lore skill install --tool {tool}", ""},
		{"choice on a plain fix", plain, onboard.ChoiceLinkReplace, "", "offers no choices"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := pickCommand(id, c.fix, onboard.CheckResult{Fix: c.fix}, c.choice)
			if c.want != "" {
				if err != nil || strings.Join(got, " ") != c.want {
					t.Fatalf("pickCommand = %v, %v; want %q", got, err, c.want)
				}
				return
			}
			if !errs.Has(err, errs.FixRefused) || !strings.Contains(err.Error(), c.refused) {
				t.Fatalf("pickCommand err = %v; want E_FIX_REFUSED with %q", err, c.refused)
			}
		})
	}
}
