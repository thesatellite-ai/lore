package team

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/schema"
)

// TestTeamRename (G-15): a team's handle changes in place (same tem_ id,
// members kept, so tasks assigned to it follow), a pending invite naming
// the old handle names the new one, and a handle a person or another team
// holds is refused.
func TestTeamRename(t *testing.T) {
	x, tomas, _ := teamFixture(t)
	added := x.teamAdd(TeamAddIn{Handle: "web", Members: []string{"@mira", "@tomas"}})
	x.teamAdd(TeamAddIn{Handle: "ops", Members: []string{"@mira"}})
	x.invite(setup.TeamInviteIn{Email: "devi@agency.example", Teams: []string{"@web"}})

	out, err := teamRename(x.ctx, x.e, TeamRenameIn{Handle: "@web", To: "@frontend"})
	if err != nil || out.Verb != verbTeamRenamed || out.From != "web" || out.Team.Handle != "frontend" || out.Team.ID != added.Team.ID {
		t.Fatalf("rename = %+v, %v", out, err)
	}
	if !slices.Contains(memberIDs(out.Team), tomas.String()) || len(out.Team.Members) != 2 {
		t.Errorf("members after rename = %+v", out.Team.Members)
	}
	if txt := out.Text(); !strings.Contains(txt, "@web is now @frontend") || !strings.Contains(txt, "devi@agency.example") {
		t.Errorf("text = %q", txt)
	}
	inv := x.e.All(schema.TableInvites)[0]
	if got := value.ItemStrings(inv.Fields[schema.FieldInviteTeams]); !slices.Equal(got, []string{"frontend"}) {
		t.Errorf("invite teams after rename = %v", got)
	}
	if _, err := teamShow(x.ctx, x.e, TeamRefIn{Handle: "web"}); !errs.Has(err, errs.NotFound) {
		t.Errorf("show of the old handle: %v", err)
	}
	for to, want := range map[string]string{"@tomas": "taken by Tomas Dev", "@ops": "taken by team", "@frontend": "already @frontend", "Bad Name": "not one lowercase word"} {
		if _, err := teamRename(x.ctx, x.e, TeamRenameIn{Handle: "frontend", To: to}); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), want) {
			t.Errorf("rename to %s: %v, want %q", to, err, want)
		}
	}
	if _, err := teamRename(x.ctx, x.e, TeamRenameIn{Handle: "nope", To: "@x"}); !errs.Has(err, errs.NotFound) {
		t.Errorf("rename of no team: %v", err)
	}
}
