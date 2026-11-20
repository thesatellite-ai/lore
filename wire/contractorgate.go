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

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// refuseOrgFolderIfContractor marks the org's org-folder stores
// write-refused when the caller's open stint is a contractor's. Best
// effort: a roster that cannot be read refuses nothing.
//
// R-16: in a code repo of the org that is not a client repo (an own repo,
// none of the contractor's projects) the repo's own notes and every
// attached folder of the org refuse the contractor's writes the same
// way; before, a contractor who cloned an own repo wrote tasks and notes
// there and their sync pushed them. clientRepo is true in a client repo
// (or where there is no code repo): the client's folders keep working.
//
// linkedFolder names the store folder the repo's org link attaches (nil
// or "": none); a cleaned linked repo's project row is there (D-736).
func (st *state) refuseOrgFolderIfContractor(ctx context.Context, attached []attach.Opened, clientRepo bool, linkedFolder func() string) {
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
			if !p.Deleted && st.isCaller(fieldText(p, schema.FieldPeriodActor)) && fieldText(p, schema.FieldPeriodEndedOn) == "" &&
				fieldText(p, schema.FieldPeriodType) == schema.PeriodTypeContractor {
				contractor = true
			}
		}
	}
	if !contractor {
		return
	}
	refusal := contractorRefusal(st.who.Org)
	ownRepo := !clientRepo && !st.memberOfRepoProject(ctx, attached, linkedFolder)
	for _, o := range attached {
		if o.Level == policy.LevelOrg || (ownRepo && o.Level != policy.LevelPersonal) {
			st.eng.RefuseWritesUnless(o.Name, refusal, ownRosterRow)
		}
	}
	if ownRepo {
		st.eng.RefuseWritesUnless(store.SourceRepo, contractorRepoRefusal(st.who.Org), ownRosterRow)
	}
}

// contractorRepoRefusal is the one line a contractor's write in an own
// repo of the org gets (R-16): the same cause as the org folder's.
func contractorRepoRefusal(org string) error {
	if org == "" {
		org = "this org"
	}
	return errs.New(errs.Refused, "you are a contractor in "+org+": this repo is one of "+org+"'s own repos, not one of your projects, so lore writes nothing here").
		WithHint("work in your project's repo (its client folder), or ask an admin of " + org + " to add you to this project")
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

// linkRosterSources gives every attached store clone with no org folder
// (a client store) the roster of the attached store that holds the org
// folder, so every reader checks a client store's rows against the
// contractor wall too (devco R-15: a contractor's hand commit in another
// client's folder was read by everyone). Nothing changes when no
// attached store holds an org folder, or when two do (two orgs are never
// mixed: no source is guessed).
func linkRosterSources(attached []attach.Opened) {
	var org *gitbackend.Backend
	for _, o := range attached {
		if o.Level == policy.LevelPersonal {
			continue
		}
		if g, ok := concrete(o.Backend).(*gitbackend.Backend); ok && g.HasOrgFolder() {
			if org != nil && org.Dir() != g.Dir() {
				return
			}
			org = g
		}
	}
	if org == nil {
		return
	}
	for _, o := range attached {
		if o.Level == policy.LevelPersonal {
			continue
		}
		if g, ok := concrete(o.Backend).(*gitbackend.Backend); ok && g.CloneMode() && !g.HasOrgFolder() {
			g.SetRosterSource(org)
		}
	}
}

// memberOfRepoProject reports whether the caller is a member (a
// project_members row in an org-level store) of a project the repo's own
// store holds: then the repo is one of their projects, not only the org's.
// A linked repo whose .lore/data holds no project row (after lore doctor
// --fix, devco D-736) is its linked folder's project: that folder's
// project rows are read instead, so a contractor on that project is not
// walled out of the repo once it is cleaned.
func (st *state) memberOfRepoProject(ctx context.Context, attached []attach.Opened, linkedFolder func() string) bool {
	projects, err := st.eng.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil {
		return false
	}
	if !liveAny(projects) && linkedFolder != nil {
		if name := linkedFolder(); name != "" {
			projects, _ = st.eng.List(ctx, store.Source(name), schema.TableProjects)
		}
	}
	if len(projects) == 0 {
		return false
	}
	here := map[string]bool{}
	for _, p := range projects {
		if !p.Deleted {
			here[p.ID.String()] = true
		}
	}
	for _, o := range attached {
		if o.Level != policy.LevelOrg {
			continue
		}
		rows, lerr := st.eng.List(ctx, o.Name, schema.TableProjectMembers)
		if lerr != nil {
			continue
		}
		for _, m := range rows {
			if !m.Deleted && st.isCaller(fieldText(m, schema.FieldMemberActor)) && here[fieldText(m, schema.FieldMemberProject)] {
				return true
			}
		}
	}
	return false
}

// isCaller reports whether actor is the caller for the contractor wall:
// the caller's actor, or the person a detected agent acts for (an agent
// has no stint of its own; before, a contractor's agent passed the wall).
func (st *state) isCaller(actor string) bool {
	if actor == "" {
		return false
	}
	if actor == st.actor.String() {
		return true
	}
	if st.who.Owner == nil {
		return false
	}
	id, err := st.who.Person().ActorID()
	return err == nil && id.String() == actor
}

// liveAny reports whether rows holds a row that is not deleted.
func liveAny(rows []row.Row) bool {
	for _, r := range rows {
		if !r.Deleted {
			return true
		}
	}
	return false
}
