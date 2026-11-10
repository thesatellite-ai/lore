package period

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// actors columns the period ops read that schema keeps unexported
// (teamprofile repeats kind the same way).
const (
	fieldKind        = "kind"
	fieldDisplayName = "display_name"
	fieldHandle      = "handle"
	fieldStableKey   = "stable_key"
	fieldOwner       = "owner_actor_id"
	fieldLabel       = "label"
	// fieldProjectName is projects.name (a membership's project label).
	fieldProjectName = "name"
)

// Output marks.
const (
	success    = "✓"
	handleMark = "@"
	arrow      = " → "
	nowLabel   = "now"
	listSep    = ", "
	clearSep   = ","
)

// confirmHint is the hint of every E_NEEDS_CONFIRM here (TP-20).
const confirmHint = "ask the person, then rerun with --yes"

// text is a field's text, "" when absent or null.
func text(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// listOf is a list field's items.
func listOf(r row.Row, name string) []string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return nil
	}
	return value.ItemStrings(v)
}

// nameOf is "Mira Patel (@mira)" for messages.
func nameOf(a row.Row) string {
	n := text(a, fieldDisplayName)
	if n == "" {
		n = a.ID.String()
	}
	if h := text(a, fieldHandle); h != "" {
		n += " (" + handleMark + h + ")"
	}
	return n
}

// isHuman reports whether a is a person (offboard and rejoin refuse the
// rest: agents follow their owner, 5.1 step 1).
func isHuman(a row.Row) bool { return text(a, fieldKind) == schema.ActorKindHuman }

// day parses a --flag date (YYYY-MM-DD); empty is def.
func day(flag, s string, def time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	t, err := time.Parse(value.DateLayout, s)
	if err != nil {
		return time.Time{}, errs.Wrap(errs.InvalidInput, "--"+flag+" "+s+" is not a date", err).WithHint("write it as YYYY-MM-DD")
	}
	return t, nil
}

// dayText is t as YYYY-MM-DD, "" for the zero time.
func dayText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(value.DateLayout)
}

// textValue is s as a text value.
func textValue(field, s string) (value.Value, error) {
	t, err := value.TextOf(s)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, field+" is not valid text", err)
	}
	return t, nil
}

// dateValue is t's day as a date value.
func dateValue(field string, t time.Time) (value.Value, error) {
	d, err := value.DateOf(teamprofile.Day(t).Format(value.DateLayout))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, field+" is out of range", err)
	}
	return d, nil
}

// periodsOf reads actor's periods from the roster (with the synthesised
// one of TP-22 when it has no row).
func periodsOf(ctx context.Context, r profilestore.Roster, actor row.Row) ([]teamprofile.Period, error) {
	rows, err := r.List(ctx, schema.TableActorPeriods)
	if err != nil {
		return nil, err
	}
	return teamprofile.PeriodsOf(actor, rows)
}

// openPeriod is the open period of ps (ok false when none).
func openPeriod(ps []teamprofile.Period) (teamprofile.Period, bool) {
	for _, p := range ps {
		if p.Open() {
			return p, true
		}
	}
	return teamprofile.Period{}, false
}

// materialise writes a synthesised period as a row (TEAM_PROFILES 8: the
// first period write materialises it) and returns it with its id. Create
// gives it the natural id (actor_id, started_on) (TS-5), so two clones
// materialising the same period converge. A period with a row is returned
// unchanged.
func materialise(ctx context.Context, r profilestore.Roster, p teamprofile.Period) (teamprofile.Period, error) {
	if !p.Synthesised() {
		return p, nil
	}
	f, err := p.Fields()
	if err != nil {
		return teamprofile.Period{}, err
	}
	w, err := r.Create(ctx, schema.TableActorPeriods, f)
	if err != nil {
		return teamprofile.Period{}, errs.Prefix("period: writing the period read from the roster (active/left_at)", err)
	}
	p.ID = w.ID
	return p, nil
}

// periodLine is "2026-01-12 → 2026-09-30  employee  Senior engineer".
func periodLine(p PeriodOut) string {
	end := p.EndedOn
	if end == "" {
		end = nowLabel
	}
	parts := []string{p.StartedOn + arrow + end, p.Type}
	if p.OrgRole != "" {
		parts = append(parts, p.OrgRole)
	}
	if p.EndReason != "" {
		parts = append(parts, "reason: "+p.EndReason)
	}
	if p.Source == schema.PeriodSourceMigrated && p.ID == "" {
		parts = append(parts, "(from the roster: active/left_at)")
	}
	return strings.Join(parts, "  ")
}

// PeriodOut is one period in output (--json and text).
type PeriodOut struct {
	// ID is empty for the period read from active/left_at (no row yet).
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	StartedOn string `json:"started_on"`
	EndedOn   string `json:"ended_on,omitempty"`
	OrgRole   string `json:"org_role,omitempty"`
	EndReason string `json:"end_reason,omitempty"`
	Note      string `json:"note,omitempty"`
	Source    string `json:"source,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// periodOut converts p.
func periodOut(p teamprofile.Period) PeriodOut {
	o := PeriodOut{Type: p.Type, StartedOn: dayText(p.StartedOn), EndedOn: dayText(p.EndedOn),
		OrgRole: p.OrgRole, EndReason: p.EndReason, Note: p.Note, Source: p.Source}
	if !p.Synthesised() {
		o.ID = p.ID.String()
	}
	return o
}

// membership is an open project membership of a person.
type membership struct {
	row     row.Row
	project string // the project's name, or its id when the row is not here
	roles   []string
	started time.Time
}

// openMemberships lists actor's open project_members rows, by project
// label. Rows are read with schema.TableProjectMembers' fields directly
// (ops/member owns the member verbs; this package only ends them, TP-16).
func openMemberships(ctx context.Context, env Env, r profilestore.Roster, actor ids.AnyID) ([]membership, error) {
	return memberships(ctx, env, r, actor, func(m row.Row) bool { return text(m, schema.FieldMemberEndedOn) == "" })
}

// memberships lists actor's project_members rows that keep says; env
// names the projects the roster store does not hold (projectLabel).
func memberships(ctx context.Context, env Env, r profilestore.Roster, actor ids.AnyID, keep func(row.Row) bool) ([]membership, error) {
	rows, err := r.List(ctx, schema.TableProjectMembers)
	if err != nil {
		return nil, err
	}
	return membershipsOf(ctx, env, r, rows, actor, keep)
}

// TrashLister is an optional upgrade of profilestore.Roster: the rows of
// table that are in the roster store's trash (List has only live ones).
// wire's roster implements it; on a roster without it, rejoin lists no
// trashed memberships (it never restores them either way).
type TrashLister interface {
	ListTrash(ctx context.Context, table row.Table) ([]row.Row, error)
}

// trashedUnstarted lists actor's memberships an offboard moved to the
// trash before they began (reason MemberTrashReasonUnstarted) and that
// are still there; none when r cannot list its trash (TrashLister).
func trashedUnstarted(ctx context.Context, env Env, r profilestore.Roster, actor ids.AnyID) ([]membership, error) {
	tl, ok := r.(TrashLister)
	if !ok {
		return nil, nil
	}
	rows, err := tl.ListTrash(ctx, schema.TableProjectMembers)
	if err != nil {
		return nil, err
	}
	return membershipsOf(ctx, env, r, rows, actor, func(m row.Row) bool {
		return text(m, trash.FieldDeletedReason) == MemberTrashReasonUnstarted
	})
}

// membershipsOf is memberships over rows already read.
func membershipsOf(ctx context.Context, env Env, r profilestore.Roster, rows []row.Row, actor ids.AnyID, keep func(row.Row) bool) ([]membership, error) {
	var out []membership
	for _, m := range rows {
		if text(m, schema.FieldMemberActor) != actor.String() || !keep(m) {
			continue
		}
		started, perr := time.Parse(value.DateLayout, text(m, schema.FieldMemberStartedOn))
		if perr != nil {
			return nil, errs.Wrap(errs.InvalidInput, "membership "+m.ID.String()+": started_on is not YYYY-MM-DD", perr)
		}
		out = append(out, membership{row: m, project: projectLabel(ctx, env, r, text(m, schema.FieldMemberProject)), roles: listOf(m, schema.FieldMemberRoles), started: started})
	}
	slices.SortFunc(out, func(a, b membership) int { return strings.Compare(a.project, b.project) })
	return out, nil
}

// projectLabel is a project's name: from the roster store when its row is
// there, else from env's profilestore.ProjectNamer upgrade (the project row
// usually lives in a project or repo store: AllowDangling on
// project_members.project_id), else its id, so a preview never fails on a
// label.
func projectLabel(ctx context.Context, env Env, r profilestore.Roster, id string) string {
	if pid, err := ids.ParseAny(id); err == nil {
		if p, gerr := r.Get(ctx, schema.TableProjects, pid); gerr == nil {
			if n := text(p, fieldProjectName); n != "" {
				return n
			}
		}
	}
	if n, ok := upgrade.As[profilestore.ProjectNamer](env); ok {
		if name, found := n.ProjectName(ctx, id); found && name != "" {
			return name
		}
	}
	return id
}

// ownedAgents lists the non-human actors owned by actor; active only when
// active is true, inactive only when false.
func ownedAgents(ctx context.Context, r profilestore.Roster, actor ids.AnyID, active bool) ([]row.Row, error) {
	all, err := r.List(ctx, schema.TableActors)
	if err != nil {
		return nil, err
	}
	var out []row.Row
	for _, a := range all {
		if a.ID == actor || isHuman(a) || text(a, fieldOwner) != actor.String() {
			continue
		}
		if teamprofile.Pickable(teamprofile.StatusOf(a.Fields)) == active {
			out = append(out, a)
		}
	}
	return out, nil
}

// agentName is "agent:auto/codex/3f9a1c (act_…)".
func agentName(a row.Row) string {
	n := text(a, fieldStableKey)
	if n == "" {
		n = text(a, fieldLabel)
	}
	if n == "" {
		n = text(a, fieldDisplayName)
	}
	return n + " (" + a.ID.String() + ")"
}
