// Package member is the project-membership and role nouns
// (TEAM_PROFILES_DESIGN 3.5, 3.6, 5.4): project.member.add|edit|remove|list
// and role.list|add|edit, with the effective role list (the built-ins of
// logic/teamprofile overlaid by roles rows, TS-2; TP-8, TP-10, TP-12,
// TP-13).
//
// Every op reads and writes the org roster store through
// profilestore.Store (TS-1) and runs direct on the roster App
// (profilestore.IsDirect); write ops open it with write=true, which refuses
// an agent unless agents.may_manage_people (TS-4).
package member

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profilestore"
)

// Env is what the member ops need; *wire.App implements it. It may also
// implement ProjectFinder (an optional upgrade).
type Env interface {
	profilestore.Store
}

// ProjectFinder is the optional upgrade that resolves a <project>
// reference across every store the App can read. Why an upgrade: a
// project row lives in its own folder or repo (2.6: project_id is
// AllowDangling), which the roster store does not hold, so the roster
// alone can resolve a prj_ id but not every project name. Without it, a
// name resolves among the roster store's own projects rows and a prj_ id
// is taken as given.
type ProjectFinder interface {
	// FindProject resolves a project name or prj_ id. No match is
	// E_NOT_FOUND; a name several projects share is
	// E_PROJECT_NAME_COLLISION (name it by its prj_ id).
	FindProject(ctx context.Context, ref string) (Project, error)
}

// Project is a resolved project: its id and name ("" when unknown).
type Project struct {
	ID   ids.ID[ids.Project]
	Name string
}

// Shown is the name, or the id when the name is unknown.
func (p Project) Shown() string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID.String()
}

// Ops returns the member and role operations bound to env (TEAM_PROFILES
// 11 A3).
func Ops(env Env) []op.Entry {
	readErrs := append([]errs.Code{errs.Refused, errs.LeakBlocked}, work.ReadErrors...)
	writeErrs := append([]errs.Code{errs.Refused, errs.LeakBlocked}, work.WriteErrors...)
	memberErrs := append([]errs.Code{errs.RoleUnknown, errs.StatusChange, errs.PeriodOverlap, errs.ProjectNameCollision}, writeErrs...)
	return []op.Entry{
		op.Define(op.Spec{Name: profilestore.OpMemberAdd, Summary: "Add a person to a project with one or more roles (re-adding starts a new stint)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: memberErrs,
			Examples: []op.Example{{Title: "add a developer who also reviews", CLI: "lore project member add acme-hub @mira --role dev,reviewer", Want: "added to acme-hub"}}},
			func(ctx context.Context, in MemberAddIn) (MemberWriteOut, error) { return memberAdd(ctx, env, in) }),
		op.Define(op.Spec{Name: profilestore.OpMemberEdit, Summary: "Set the roles of a current project member",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: memberErrs,
			Examples: []op.Example{{Title: "change roles", CLI: "lore project member edit acme-hub @mira --role dev", Want: "roles now dev"}}},
			func(ctx context.Context, in MemberEditIn) (MemberWriteOut, error) { return memberEdit(ctx, env, in) }),
		op.Define(op.Spec{Name: profilestore.OpMemberRemove, Summary: "End a project membership on a date (the row and its history are kept)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: memberErrs,
			Examples: []op.Example{{Title: "someone moves to another project", CLI: `lore project member remove acme-hub @mira --reason "moved to northwind"`, Want: "removed from acme-hub"}}},
			func(ctx context.Context, in MemberRemoveIn) (MemberWriteOut, error) {
				return memberRemove(ctx, env, in)
			}),
		op.Define(op.Spec{Name: profilestore.OpMemberList, Summary: "List project members (--history adds ended stints)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: append([]errs.Code{errs.ProjectNameCollision}, readErrs...),
			Examples: []op.Example{{Title: "members of a project, with past stints", CLI: "lore project member list acme-hub --history", Want: "@"}}},
			func(ctx context.Context, in MemberListIn) (MemberListOut, error) { return memberList(ctx, env, in) }),
		op.Define(op.Spec{Name: profilestore.OpRoleList, Summary: "List the project roles (built-ins and added ones)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrs,
			Examples: []op.Example{{Title: "the roles", CLI: "lore role list", Want: "dev"}}},
			func(ctx context.Context, in RoleListIn) (RoleListOut, error) { return roleList(ctx, env, in) }),
		op.Define(op.Spec{Name: profilestore.OpRoleAdd, Summary: "Add a project role",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append([]errs.Code{errs.NameTaken}, writeErrs...),
			Examples: []op.Example{{Title: "a new role", CLI: `lore role add tech_writer --label "Technical writer"`, Want: "role tech_writer added"}}},
			func(ctx context.Context, in RoleAddIn) (RoleWriteOut, error) { return roleAdd(ctx, env, in) }),
		op.Define(op.Spec{Name: profilestore.OpRoleEdit, Summary: "Change a role's label or description, or retire it",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append([]errs.Code{errs.RoleUnknown}, writeErrs...),
			Examples: []op.Example{{Title: "retire a role", CLI: "lore role edit client --retire", Want: "role client updated"}}},
			func(ctx context.Context, in RoleEditIn) (RoleWriteOut, error) { return roleEdit(ctx, env, in) }),
	}
}

// finder is env's ProjectFinder upgrade, nil when it has none.
func finder(env Env) ProjectFinder {
	if f, ok := upgrade.As[ProjectFinder](env); ok {
		return f
	}
	return nil
}

// boolTrue is the text form of a true bool field.
const boolTrue = "true"

// roleSep separates role names in one --role value ("dev,reviewer").
const roleSep = ","

// handleMark prefixes a handle in output.
const handleMark = "@"

// fieldText is a field's text, "" when absent or null.
func fieldText(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// textFields turns the non-empty strings of m into text values.
func textFields(m map[string]string) (map[string]value.Value, error) {
	out := make(map[string]value.Value, len(m))
	for k, s := range m {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		t, err := value.TextOf(s)
		if err != nil {
			return nil, errs.Wrap(errs.InvalidInput, k+" is not valid text", err)
		}
		out[k] = t
	}
	return out, nil
}

// parseRowID parses a row id read back from a row.
func parseRowID(s string) (ids.AnyID, error) {
	id, err := ids.ParseAny(s)
	if err != nil {
		return ids.AnyID{}, errs.Wrap(errs.Internal, "row id "+s+" is not valid", err)
	}
	return id, nil
}
