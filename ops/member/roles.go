package member

import (
	"context"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"regexp"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// Roles (TEAM_PROFILES 2.4, 3.6, TP-8, TS-2): the effective role list is
// the compiled-in roles of logic/teamprofile with every live roles row
// laid over them by name. A row named like a built-in changes its label,
// description or retired flag; a new name adds a role. Nothing is seeded:
// `role edit dev --label …` on a built-in with no row writes the row then.
//
// Flow:
//
//	role.list ──▶ Roster(read)  ──▶ effectiveRoles ──▶ live (or all with --retired)
//	role.add  ──▶ Roster(write) ──▶ name free?    ──▶ Create roles row ──▶ Push
//	role.edit ──▶ Roster(write) ──▶ name known?   ──▶ Update the row, or Create it
//	                                                   over a built-in   ──▶ Push

// roleName is the shape of a role name: snake_case, the key memberships
// store (never renamed, 2.4).
var roleName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// roleHint is the next step of E_ROLE_UNKNOWN (TEAM_PROFILES 4).
const roleHint = "lore role list; lore role add --name <name>"

// RoleOut is one effective role.
type RoleOut struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Retired     bool   `json:"retired"`
	// Builtin: compiled in (TP-8); a row may still change it.
	Builtin bool `json:"builtin"`
	// ID is the roles row, "" for a built-in no row changes.
	ID string `json:"id,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// effectiveRoles lays the live roles rows over the built-ins by name
// (TS-2): built-ins first in their declared order, then the added roles by
// name. A row's empty label or description keeps the built-in's; its
// retired flag always wins (false un-retires).
func effectiveRoles(rows []row.Row) []RoleOut {
	out := make([]RoleOut, 0, len(rows)+len(teamprofile.BuiltinRoles()))
	at := map[string]int{}
	for _, b := range teamprofile.BuiltinRoles() {
		at[b.Name] = len(out)
		out = append(out, RoleOut{Name: b.Name, Label: b.Label, Description: b.Description, Retired: b.Retired, Builtin: true})
	}
	var added []RoleOut
	for _, r := range rows {
		if r.Deleted {
			continue
		}
		name := fieldText(r, schema.FieldRoleName)
		if name == "" {
			continue
		}
		over := RoleOut{Name: name, Label: fieldText(r, schema.FieldRoleLabel), Description: fieldText(r, schema.FieldRoleDescription),
			Retired: fieldText(r, schema.FieldRoleRetired) == boolTrue, ID: r.ID.String(),
			CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy}
		i, ok := at[name]
		if !ok {
			added = append(added, over)
			continue
		}
		cur := &out[i]
		if over.Label != "" {
			cur.Label = over.Label
		}
		if over.Description != "" {
			cur.Description = over.Description
		}
		cur.Retired, cur.ID = over.Retired, over.ID
	}
	slices.SortFunc(added, func(a, b RoleOut) int { return strings.Compare(a.Name, b.Name) })
	return append(out, added...)
}

// findRole is the effective role named name.
func findRole(roles []RoleOut, name string) (RoleOut, bool) {
	for _, r := range roles {
		if r.Name == name {
			return r, true
		}
	}
	return RoleOut{}, false
}

// readRoles opens nothing: it lists the roles rows of an open roster and
// returns the effective list.
func readRoles(ctx context.Context, r profilestore.Roster) ([]RoleOut, error) {
	rows, err := r.List(ctx, schema.TableRoles)
	if err != nil {
		return nil, err
	}
	return effectiveRoles(rows), nil
}

// parseRoles splits --role values ("dev,reviewer", repeatable) into names,
// trimmed, without duplicates, in first-seen order. None is E_INVALID_INPUT
// (a membership holds at least one role, TP-10).
func parseRoles(in []string) ([]string, error) {
	var out []string
	for _, v := range in {
		for _, p := range strings.Split(v, roleSep) {
			p = strings.TrimSpace(p)
			if p != "" && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		return nil, errs.New(errs.InvalidInput, "no --role: a membership holds at least one role").WithHint("--role dev or --role dev,reviewer; " + roleHint)
	}
	return out, nil
}

// checkRoles refuses any name in want that is not live in roles
// (E_ROLE_UNKNOWN): unknown, or retired (2.4: retired is out of
// `member add --role`). keep are names the membership already holds:
// a retired role it holds stays allowed (existing memberships keep it).
func checkRoles(roles []RoleOut, want, keep []string) error {
	var bad []string
	for _, n := range want {
		r, ok := findRole(roles, n)
		if ok && (!r.Retired || slices.Contains(keep, n)) {
			continue
		}
		why := n + " (unknown)"
		if ok {
			why = n + " (retired)"
		}
		bad = append(bad, why)
	}
	if len(bad) == 0 {
		return nil
	}
	return errs.New(errs.RoleUnknown, "not a live role: "+strings.Join(bad, ", ")).WithHint(roleHint)
}

// RoleListIn is the input of role.list.
type RoleListIn struct {
	Retired bool `json:"retired,omitempty" flag:"retired" desc:"include retired roles"`
	// --created-by / --updated-by: the shared writer filters of every
	// noun list (INTEG-AUDIT-1; engine.List applies them).
	metalist.Audit
	// --limit / --page: the shared paging flags of every list (op.PageFlags, T-4).
	op.PageFlags
}

// RoleListOut is the effective role list.
type RoleListOut struct {
	Roles []RoleOut `json:"roles"`
}

// Text prints one role per line: "dev  Developer — writes the code".
func (o RoleListOut) Text() string {
	var b strings.Builder
	for _, r := range o.Roles {
		b.WriteString(r.Name)
		if r.Label != "" {
			b.WriteString("  " + r.Label)
		}
		if r.Description != "" {
			b.WriteString(" — " + r.Description)
		}
		if r.Retired {
			b.WriteString(" (retired)")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func roleList(ctx context.Context, env Env, in RoleListIn) (RoleListOut, error) {
	r, done, err := env.Roster(ctx, false)
	defer done()
	if err != nil {
		return RoleListOut{}, err
	}
	roles, err := readRoles(ctx, r)
	if err != nil {
		return RoleListOut{}, err
	}
	out := RoleListOut{Roles: []RoleOut{}}
	for _, x := range roles {
		if in.Retired || !x.Retired {
			out.Roles = append(out.Roles, x)
		}
	}
	return out, nil
}

// RoleAddIn is the input of role.add.
type RoleAddIn struct {
	Name        string `json:"name" arg:"0" desc:"the role name, snake_case (never renamed)"`
	Label       string `json:"label,omitempty" flag:"label" desc:"the shown name"`
	Description string `json:"description,omitempty" flag:"description" desc:"one line"`
}

// RoleWriteOut is the role after role.add or role.edit.
type RoleWriteOut struct {
	Role RoleOut `json:"role"`
	// Verb is "added" or "updated".
	Verb  string `json:"verb"`
	Where string `json:"where"`
	// Warnings are the engine's warnings of the write
	// (profilestore.WarningLine), printed after the success line.
	Warnings []string `json:"warnings,omitempty"`
}

// Text is "✓ role designer_lead added in agency org/".
func (o RoleWriteOut) Text() string { return o.text() + profilestore.WarningText(o.Warnings) }

// text is Text without the warnings.
func (o RoleWriteOut) text() string {
	return work.Success + " role " + o.Role.Name + " " + o.Verb + " in " + o.Where + "\n"
}

// Role write verbs.
const (
	verbAdded   = "added"
	verbUpdated = "updated"
)

func roleAdd(ctx context.Context, env Env, in RoleAddIn) (RoleWriteOut, error) {
	name := strings.TrimSpace(in.Name)
	if !roleName.MatchString(name) {
		return RoleWriteOut{}, errs.New(errs.InvalidInput, "role name "+work.Quote(in.Name)+" is not snake_case").WithHint("lore role add --name tech_lead")
	}
	r, done, err := env.Roster(ctx, true)
	defer done()
	if err != nil {
		return RoleWriteOut{}, err
	}
	roles, err := readRoles(ctx, r)
	if err != nil {
		return RoleWriteOut{}, err
	}
	if cur, ok := findRole(roles, name); ok {
		hint := "lore role edit --name " + name + " --label …"
		if cur.Retired {
			hint = "lore role edit --name " + name + " --unretire"
		}
		return RoleWriteOut{}, errs.New(errs.NameTaken, "role "+name+" already exists").WithHint(hint)
	}
	fields, err := textFields(map[string]string{schema.FieldRoleName: name, schema.FieldRoleLabel: in.Label, schema.FieldRoleDescription: in.Description})
	if err != nil {
		return RoleWriteOut{}, err
	}
	w, err := r.Create(ctx, schema.TableRoles, fields)
	if err != nil {
		return RoleWriteOut{}, err
	}
	r.Push(ctx)
	return RoleWriteOut{Role: RoleOut{Name: name, Label: in.Label, Description: in.Description, ID: w.ID.String()}, Verb: verbAdded, Where: r.Where(), Warnings: r.Warnings()}, nil
}

// RoleEditIn is the input of role.edit.
type RoleEditIn struct {
	Name        string `json:"name" arg:"0" desc:"the role name"`
	Label       string `json:"label,omitempty" flag:"label" desc:"the shown name"`
	Description string `json:"description,omitempty" flag:"description" desc:"one line"`
	Retire      bool   `json:"retire,omitempty" flag:"retire" desc:"take it out of member add --role (existing memberships keep it)"`
	Unretire    bool   `json:"unretire,omitempty" flag:"unretire" desc:"make a retired role live again"`
}

func roleEdit(ctx context.Context, env Env, in RoleEditIn) (RoleWriteOut, error) {
	name := strings.TrimSpace(in.Name)
	if in.Retire && in.Unretire {
		return RoleWriteOut{}, errs.New(errs.InvalidInput, "--retire and --unretire together").WithHint("pass one")
	}
	patch, err := textFields(map[string]string{schema.FieldRoleLabel: in.Label, schema.FieldRoleDescription: in.Description})
	if err != nil {
		return RoleWriteOut{}, err
	}
	if in.Retire || in.Unretire {
		patch[schema.FieldRoleRetired] = value.BoolOf(in.Retire)
	}
	if len(patch) == 0 {
		return RoleWriteOut{}, errs.New(errs.InvalidInput, "role edit "+name+": nothing to change").WithHint("--label, --description, --retire or --unretire")
	}
	r, done, err := env.Roster(ctx, true)
	defer done()
	if err != nil {
		return RoleWriteOut{}, err
	}
	roles, err := readRoles(ctx, r)
	if err != nil {
		return RoleWriteOut{}, err
	}
	cur, ok := findRole(roles, name)
	if !ok {
		return RoleWriteOut{}, errs.New(errs.RoleUnknown, "no role "+work.Quote(name)).WithHint(roleHint)
	}
	if cur.ID != "" {
		id, perr := parseRowID(cur.ID)
		if perr != nil {
			return RoleWriteOut{}, perr
		}
		if _, err = r.Update(ctx, schema.TableRoles, id, patch); err != nil {
			return RoleWriteOut{}, err
		}
	} else {
		// A built-in with no row yet: the row is written now, holding only
		// the name and what changed (TS-2: the rest still reads from the
		// built-in).
		n, terr := value.TextOf(name)
		if terr != nil {
			return RoleWriteOut{}, errs.Wrap(errs.InvalidInput, "role name", terr)
		}
		patch[schema.FieldRoleName] = n
		w, cerr := r.Create(ctx, schema.TableRoles, patch)
		if cerr != nil {
			return RoleWriteOut{}, cerr
		}
		cur.ID = w.ID.String()
	}
	r.Push(ctx)
	if in.Label != "" {
		cur.Label = in.Label
	}
	if in.Description != "" {
		cur.Description = in.Description
	}
	if in.Retire || in.Unretire {
		cur.Retired = in.Retire
	}
	return RoleWriteOut{Role: cur, Verb: verbUpdated, Where: r.Where(), Warnings: r.Warnings()}, nil
}
