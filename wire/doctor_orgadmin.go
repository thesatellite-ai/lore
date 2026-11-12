package wire

// doctor_orgadmin.go — the org's creator holds org role admin (owner
// report 2026-11-12: `lore member list` showed "-" for the person who
// created the org). The admin setup step writes it for a new org; for an
// org made before, `lore doctor` reports it (org_admin_missing) and
// `lore doctor --fix` sets it on the creator's open stint (writing one
// when the stint is only implied by the actor row).
//
//	creator = who created the org folder's project row (as orgAdmin reads it)
//	open stored stint with org_role ─▶ nothing
//	open stored stint without one   ─▶ warning; --fix: org_role admin
//	no stored stint                 ─▶ warning; --fix: an employee stint
//	                                   from the actor's first day, admin

import (
	"context"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	opsteam "github.com/thesatellite-ai/lore/ops/team"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckOrgAdmin names the check.
const DoctorCheckOrgAdmin DoctorCheck = "org_admin_missing"

// DoctorOrgAdmin is the check (registered in doctor_op.go). It reads the
// roster of each org this machine holds an admin setup of (the person who
// can fix it), as the setup step reads it, so it runs from any folder.
func (a *App) DoctorOrgAdmin(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	cfg, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return nil, nil
	}
	var out []DoctorFinding
	seen := map[string]bool{}
	for _, e := range cfg.Setups {
		if e.Kind != onboard.KindAdmin || e.Org == "" || seen[e.Org] {
			continue
		}
		seen[e.Org] = true
		if f, ok := a.orgAdminFinding(ctx, orgRosterStore{a: a, org: e.Org}, fix); ok {
			out = append(out, f)
		}
	}
	return out, nil
}

// orgAdminFinding is the check on one org's roster.
func (a *App) orgAdminFinding(ctx context.Context, rs orgRosterStore, fix bool) (DoctorFinding, bool) {
	ro, closeRead, err := rs.Roster(ctx, false)
	done := sync.OnceFunc(closeRead)
	defer done()
	if err != nil {
		return DoctorFinding{}, false
	}
	p, ok := ro.(*profileRoster)
	if !ok || p.at.src == store.SourceRepo {
		return DoctorFinding{}, false
	}
	pid, err := folderProjectIDOf(ctx, p.st, p.at.src)
	if err != nil || pid.IsZero() {
		return DoctorFinding{}, false
	}
	proj, err := p.Get(ctx, schema.TableProjects, pid.Any())
	if err != nil || proj.CreatedBy.IsZero() {
		return DoctorFinding{}, false
	}
	creator := proj.CreatedBy.String()
	actor, err := p.Get(ctx, schema.TableActors, proj.CreatedBy.Any())
	if err != nil || fieldText(actor, actorFieldKind) != schema.ActorKindHuman {
		return DoctorFinding{}, false
	}
	periods, err := p.List(ctx, schema.TableActorPeriods)
	if err != nil {
		return DoctorFinding{}, false
	}
	var open *row.Row
	for i, x := range periods {
		if fieldText(x, schema.FieldPeriodActor) == creator && fieldText(x, schema.FieldPeriodEndedOn) == "" {
			open = &periods[i]
			break
		}
	}
	if open != nil && fieldText(*open, schema.FieldPeriodOrgRole) != "" {
		return DoctorFinding{}, false
	}
	f := DoctorFinding{Check: DoctorCheckOrgAdmin, Level: DoctorWarning, Store: p.at.src,
		Message: fieldText(actor, actorFieldDisplay) + " (" + creator + ") created org " + rs.org + " but has no org role (member list shows \"-\"); the org's creator is its admin",
		Fix:     "lore doctor --fix (or: lore member edit " + creator + " --role admin)"}
	if !fix {
		return f, true
	}
	done()
	w, wdone, err := rs.Roster(ctx, true)
	defer wdone()
	if err == nil {
		err = setAdminRole(ctx, w, actor, open, a.opts.Wall())
	}
	if err != nil {
		f.Message += " (not fixed: " + err.Error() + ")"
		return f, true
	}
	w.Push(ctx)
	f.Fixed = true
	return f, true
}

// setAdminRole puts org role admin on the open stint, or writes an open
// employee stint from the actor's first day with it.
func setAdminRole(ctx context.Context, r profilestore.Roster, actor row.Row, open *row.Row, now time.Time) error {
	role, err := value.TextOf(opsteam.OrgRoleAdmin)
	if err != nil {
		return err
	}
	if open != nil {
		_, err = r.Update(ctx, schema.TableActorPeriods, open.ID, map[string]value.Value{schema.FieldPeriodOrgRole: role})
		return err
	}
	ref, err := value.RefOf(actor.ID)
	if err != nil {
		return err
	}
	typ, err := value.TextOf(schema.PeriodTypeEmployee)
	if err != nil {
		return err
	}
	first := actor.CreatedAt
	if first.IsZero() {
		first = now
	}
	day, err := value.Parse(value.KindDate, []byte(`"`+first.UTC().Format(time.DateOnly)+`"`))
	if err != nil {
		return err
	}
	_, err = r.Create(ctx, schema.TableActorPeriods, map[string]value.Value{schema.FieldPeriodActor: ref, schema.FieldPeriodType: typ,
		schema.FieldPeriodStartedOn: day, schema.FieldPeriodOrgRole: role})
	return err
}
