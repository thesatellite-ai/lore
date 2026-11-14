package member

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestStintBoundaryAlikeForMembersAndPeriods (profiles open item 6): a
// project member re-added could start on the old stint's last day while a
// company period could not. Both now share one boundary
// (teamprofile.Span.Overlaps): a new stint starting on the old one's last
// day is E_PERIOD_OVERLAP for a membership and for a company period, and
// the day after is accepted by both.
func TestStintBoundaryAlikeForMembersAndPeriods(t *testing.T) {
	const start, end, next = "2026-03-01", "2026-06-30", "2026-07-01"
	day := func(s string) time.Time {
		d, err := time.Parse(value.DateLayout, s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, c := range []struct {
		on   string
		want bool // refused
	}{{end, true}, {next, false}} {
		t.Run("on "+c.on, func(t *testing.T) {
			// A project membership re-added from c.on.
			x := newFixture(t)
			pref, _ := value.RefOf(x.hub.Any())
			aref, _ := value.RefOf(x.mira.Any())
			roles, err := rolesValue([]string{"dev"})
			if err != nil {
				t.Fatal(err)
			}
			s, _ := value.DateOf(start)
			e, _ := value.DateOf(end)
			x.f.Put(row.Row{Table: schema.TableProjectMembers, ID: testID[ids.ProjectMember](t, ids.PrefixProjectMember).Any(),
				Fields: map[string]value.Value{schema.FieldMemberProject: pref, schema.FieldMemberActor: aref,
					schema.FieldMemberRoles: roles, schema.FieldMemberStartedOn: s, schema.FieldMemberEndedOn: e}})
			_, merr := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}, On: c.on})

			// A company period from c.on after the same ended one.
			perr := teamprofile.CheckPeriods([]teamprofile.Period{
				{Type: "employee", StartedOn: day(start), EndedOn: day(end)},
				{Type: "employee", StartedOn: day(c.on)},
			})

			for what, err := range map[string]error{"membership": merr, "company period": perr} {
				if got := errs.Has(err, errs.PeriodOverlap); got != c.want {
					t.Errorf("%s from %s after a stint %s → %s: refused %v, want %v (%v)", what, c.on, start, end, got, c.want, err)
				}
			}
		})
	}
}
