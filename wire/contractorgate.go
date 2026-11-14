package wire

// contractorgate.go — a contractor writes only to their client folders
// (J-15). A person whose open stint on the org roster has the type
// contractor (`lore team invite --type contractor --project <p>`) could
// add company rules and rename the company's teams: every member wrote
// to the org folder (company rules, teams, the roster). Now:
//
//	open ──▶ canonicalActor ──▶ refuseOrgFolderIfContractor: the caller's
//	         open stint in the org folder store is a contractor's?
//	           yes ─▶ every attached store at Level org refuses writes
//	                  with one E_REFUSED line; project folders (the
//	                  client's) and the personal store keep working
//	           no  ─▶ nothing changes
//
// The roster on this machine is the one last synced, as for leftgate.go.

import (
	"context"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// refuseOrgFolderIfContractor marks the org's org-folder stores
// write-refused when the caller's open stint is a contractor's. Best
// effort: a roster that cannot be read refuses nothing.
func (st *state) refuseOrgFolderIfContractor(ctx context.Context, attached []attach.Opened) {
	if st.eng == nil || st.actor.IsZero() {
		return
	}
	contractor := false
	for _, o := range attached {
		if o.Level != policy.LevelOrg {
			continue
		}
		rows, err := st.eng.List(ctx, o.Name, schema.TableActorPeriods)
		if err != nil {
			continue
		}
		for _, p := range rows {
			if !p.Deleted && fieldText(p, schema.FieldPeriodActor) == st.actor.String() && fieldText(p, schema.FieldPeriodEndedOn) == "" &&
				fieldText(p, schema.FieldPeriodType) == schema.PeriodTypeContractor {
				contractor = true
			}
		}
	}
	if !contractor {
		return
	}
	refusal := contractorRefusal(st.who.Org)
	for _, o := range attached {
		if o.Level == policy.LevelOrg {
			st.eng.RefuseWritesUnless(o.Name, refusal, ownRosterRow)
		}
	}
}

// ownRosterRowKey marks a write of the caller's own roster row.
type ownRosterRowKey struct{}

// withOwnRosterRow is ctx for the caller's write of their own actor row
// (lore actor register, a person) or of an agent they own (lore agent
// start): a contractor's org folder lets it through, as it is the
// contractor's own identity on the roster.
func withOwnRosterRow(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownRosterRowKey{}, true)
}

// ownRosterRow reports whether ctx is withOwnRosterRow's.
func ownRosterRow(ctx context.Context) bool {
	v, _ := ctx.Value(ownRosterRowKey{}).(bool)
	return v
}

// contractorRefusal is the one line a contractor's write to the org
// folder gets.
func contractorRefusal(org string) error {
	if org == "" {
		org = "this org"
	}
	return errs.New(errs.Refused, "you are a contractor in "+org+": you write only to your projects' folders, not to "+org+"'s own (company rules, teams, the roster)").
		WithHint("write with --to <your project's folder>, or ask an admin of " + org + " to make the change")
}
