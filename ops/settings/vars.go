package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Variable operations (CLI_REFERENCE 3.12): one `vars` row (var_) per
// variable, used as ${var:<name>} in text. A name resolves to the nearest
// definition (repo, then folder, then store).
const (
	OpVarsList   = "vars.list"
	OpVarsShow   = "vars.show"
	OpVarsSet    = "vars.set"
	OpVarsRename = "vars.rename"
	OpVarsRm     = "vars.rm"
	OpVarsWhere  = "vars.where"
)

// vars field names (schema.Builtin).
const (
	vName        = "name"
	vType        = "type"
	vValue       = "value"
	vShareable   = "shareable"
	vAliases     = "aliases"
	vDescription = "description"
)

// varTypes are the types a variable may have (CLI_REFERENCE 3.12; the
// shared value types; secret_ref is a text holding a vault locator).
func varTypes() []string {
	return []string{"text", "number", "url", "dns", "ip", "cidr", "email", "date", "datetime", "duration", varTypeSecretRef}
}

// varTypeSecretRef is a variable that holds a vault locator, never a value.
const varTypeSecretRef = "secret_ref"

// VarsListIn is the input of vars.list.
type VarsListIn struct {
	To  string `json:"to,omitempty" flag:"to" desc:"only this place's variables"`
	All bool   `json:"all,omitempty" flag:"all" desc:"also list definitions hidden by a nearer one"`
}

// VarsShowIn is the input of vars.show.
type VarsShowIn struct {
	Name string `json:"id" arg:"0" desc:"the variable (name or var_ id)"`
	To   string `json:"to,omitempty" flag:"to" desc:"the definition in this place"`
}

// VarsSetIn is the input of vars.set.
type VarsSetIn struct {
	Name        string `json:"name" arg:"0" desc:"the variable name (snake_case)"`
	Value       string `json:"value" arg:"1" desc:"the value"`
	To          string `json:"to,omitempty" flag:"to" desc:"where the variable lives (repo, me, <store>, <store>/<folder>; default repo)"`
	Shareable   bool   `json:"shareable,omitempty" flag:"shareable" desc:"resolves for everyone who reads this repo"`
	Type        string `json:"type,omitempty" flag:"type" desc:"text | number | url | dns | ip | cidr | email | date | datetime | duration | secret_ref (default text)"`
	Description string `json:"description,omitempty" flag:"description" desc:"what the variable is"`
}

// VarsRenameIn is the input of vars.rename.
type VarsRenameIn struct {
	Name string `json:"id" arg:"0" desc:"the variable (name or var_ id)"`
	New  string `json:"new_name" arg:"1" desc:"the new name (the old one stays an alias)"`
	To   string `json:"to,omitempty" flag:"to" desc:"the definition in this place"`
}

// VarsRmIn is the input of vars.rm.
type VarsRmIn struct {
	Name string `json:"id" arg:"0" desc:"the variable (name or var_ id)"`
	To   string `json:"to,omitempty" flag:"to" desc:"the definition in this place"`
}

// Var is one variable as shown.
type Var struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Value       string   `json:"value"`
	Store       string   `json:"store"`
	Shareable   bool     `json:"shareable,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	Description string   `json:"description,omitempty"`
	// Hidden marks a definition a nearer one with the same name hides.
	Hidden bool `json:"hidden,omitempty"`
}

// VarsOut is the output of vars.list and vars.where.
type VarsOut struct {
	Count int   `json:"count"`
	Data  []Var `json:"data"`
}

// Text implements op.Texter.
func (o VarsOut) Text() string {
	if len(o.Data) == 0 {
		return "(no variables)\n"
	}
	var b strings.Builder
	b.WriteString("NAME\tVALUE\tTYPE\tWHERE\n")
	for _, v := range o.Data {
		where := v.Store
		if v.Shareable {
			where += " (shareable)"
		}
		if v.Hidden {
			where += " (hidden)"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", v.Name, v.Value, v.Type, where)
	}
	return b.String()
}

// VarOut is the output of vars.show.
type VarOut struct {
	Data Var `json:"data"`
}

// Text implements op.Texter.
func (o VarOut) Text() string {
	v := o.Data
	s := fmt.Sprintf("%s %s = %s (%s) in %s\n", v.ID, v.Name, v.Value, v.Type, v.Store)
	if v.Description != "" {
		s += "  " + v.Description + "\n"
	}
	if len(v.Aliases) > 0 {
		s += "  aliases: " + strings.Join(v.Aliases, ", ") + "\n"
	}
	return s
}

func (o ops) varOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpVarsList, Summary: "List project variables (nearest definition per name)", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors}, o.varsList),
		op.Define(op.Spec{Name: OpVarsShow, Summary: "Show one variable", Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors}, o.varsShow),
		op.Define(op.Spec{Name: OpVarsSet, Summary: "Set a variable (adds it, or changes its value in that place)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "a dns variable", CLI: "lore vars set db_host db.example.com --type dns", Want: "var_"}}}, o.varsSet),
		op.Define(op.Spec{Name: OpVarsRename, Summary: "Rename a variable (the old name stays an alias)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors}, o.varsRename),
		op.Define(op.Spec{Name: OpVarsRm, Summary: "Remove a variable (a tombstone is kept)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: op.ConfirmRule{MayAsk: true}}, o.varsRm),
		op.Define(op.Spec{Name: OpVarsWhere, Summary: "Every place that defines a variable", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors}, o.varsWhere),
	}
}

func varOf(l located) Var {
	return Var{ID: l.Row.ID.String(), Name: text(l.Row, vName), Type: text(l.Row, vType), Value: display(fromJSON(l.Row.Fields[vValue])),
		Store: string(l.Src), Shareable: flag(l.Row, vShareable), Aliases: texts(l.Row, vAliases), Description: text(l.Row, vDescription)}
}

func (o ops) varsList(ctx context.Context, in VarsListIn) (VarsOut, error) {
	rows, err := liveRows(ctx, o.env, sources(o.env, in.To), schema.TableVars)
	if err != nil {
		return VarsOut{}, err
	}
	out := VarsOut{Data: []Var{}}
	seen := map[string]bool{}
	for _, l := range rows {
		v := varOf(l)
		v.Hidden = seen[v.Name]
		seen[v.Name] = true
		if v.Hidden && !in.All {
			continue
		}
		out.Data = append(out.Data, v)
	}
	out.Count = len(out.Data)
	return out, nil
}

// varByName resolves a name (or alias, or id) to the nearest definition.
func (o ops) varByName(ctx context.Context, key, to string) (located, error) {
	rows, err := liveRows(ctx, o.env, sources(o.env, to), schema.TableVars)
	if err != nil {
		return located{}, err
	}
	for _, match := range []func(located) bool{
		func(l located) bool { return l.Row.ID.String() == key || text(l.Row, vName) == key },
		func(l located) bool { return slices.Contains(texts(l.Row, vAliases), key) },
	} {
		for _, l := range rows {
			if match(l) {
				return l, nil
			}
		}
	}
	return located{}, errs.New(errs.NotFound, "no variable "+quote(key)).WithHint("run `lore vars list` to see the variables")
}

func (o ops) varsShow(ctx context.Context, in VarsShowIn) (VarOut, error) {
	l, err := o.varByName(ctx, in.Name, in.To)
	if err != nil {
		return VarOut{}, err
	}
	return VarOut{Data: varOf(l)}, nil
}

func (o ops) varsSet(ctx context.Context, in VarsSetIn) (RowOut, error) {
	if !snakeName.MatchString(in.Name) {
		return RowOut{}, errs.New(errs.InvalidInput, "variable name "+quote(in.Name)+" is not snake_case")
	}
	typ := in.Type
	if typ == "" {
		typ = string(value.KindText)
	}
	if !slices.Contains(varTypes(), typ) {
		return RowOut{}, errs.New(errs.InvalidInput, "--type "+quote(typ)+" is not one of "+strings.Join(varTypes(), ", "))
	}
	kind := value.Kind(typ)
	if typ == varTypeSecretRef {
		kind = value.KindText
	}
	v, err := parseAs(kind, in.Value)
	if err != nil {
		return RowOut{}, errs.Wrap(errs.InvalidInput, "vars set: "+quote(in.Value)+" is not a "+typ, err)
	}
	// value is a json column (spec 4.3); the engine scans it like text.
	jv, err := asJSON(v)
	if err != nil {
		return RowOut{}, err
	}
	fields := map[string]value.Value{vValue: jv, vShareable: value.BoolOf(in.Shareable)}
	if err := optTexts(fields, map[string]string{vName: in.Name, vType: typ, vDescription: in.Description}); err != nil {
		return RowOut{}, err
	}
	target := targetOf(in.To)
	rows, err := liveRows(ctx, o.env, sources(o.env, string(target)), schema.TableVars)
	if err != nil {
		return RowOut{}, err
	}
	if hit := where(rows, map[string]string{vName: in.Name}); len(hit) > 0 {
		delete(fields, vName)
		w, uerr := o.env.Update(ctx, engine.Ref{Store: target, Table: schema.TableVars, ID: hit[0].Row.ID}, fields)
		if uerr != nil {
			return RowOut{}, uerr
		}
		return RowOut{ID: w.Row.ID.String(), Store: string(target),
			Human: fmt.Sprintf("Changed %s %s = %s (%s) %s.\n", short(w.Row.ID), in.Name, in.Value, typ, varWhere(target, in.Shareable))}, nil
	}
	w, err := create(ctx, o.env, target, schema.TableVars, map[string]string{vName: in.Name}, fields, freshID[ids.Var]())
	if err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(target),
		Human: fmt.Sprintf("Added %s %s = %s (%s) %s.\n", short(w.Row.ID), in.Name, in.Value, typ, varWhere(target, in.Shareable))}, nil
}

// varWhere is the "where and who sees it" tail of a vars set line.
func varWhere(target store.Source, shareable bool) string {
	if shareable {
		return "in " + string(target) + " (shareable: resolves for everyone with access)"
	}
	return "in " + string(target) + " (not shareable: clients see ${var:<name>})"
}

func (o ops) varsRename(ctx context.Context, in VarsRenameIn) (RowOut, error) {
	if !snakeName.MatchString(in.New) {
		return RowOut{}, errs.New(errs.InvalidInput, "variable name "+quote(in.New)+" is not snake_case")
	}
	l, err := o.varByName(ctx, in.Name, in.To)
	if err != nil {
		return RowOut{}, err
	}
	if hit := where([]located{l}, map[string]string{vName: in.New}); len(hit) > 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "the variable is already named "+in.New)
	}
	if other, oerr := o.varByName(ctx, in.New, string(l.Src)); oerr == nil && other.Row.ID != l.Row.ID {
		return RowOut{}, errs.New(errs.InvalidInput, "a variable named "+quote(in.New)+" already exists in "+string(l.Src))
	}
	old := text(l.Row, vName)
	aliases := append(texts(l.Row, vAliases), old)
	al, err := textList(aliases)
	if err != nil {
		return RowOut{}, err
	}
	nv, err := txt(in.New)
	if err != nil {
		return RowOut{}, err
	}
	patch := map[string]value.Value{vName: nv, vAliases: al}
	// A stored text value reads back as text, not json: re-type it so the
	// whole-row validation of the update accepts it (later.md: the engine
	// should re-type stored fields by the schema itself).
	if patch[vValue], err = asJSON(fromJSON(l.Row.Fields[vValue])); err != nil {
		return RowOut{}, err
	}
	return o.patchRow(ctx, l, patch, "renamed variable "+old+" → "+in.New+" (old name kept as an alias)")
}

func (o ops) varsRm(ctx context.Context, in VarsRmIn) (RowOut, error) {
	l, err := o.varByName(ctx, in.Name, in.To)
	if err != nil {
		return RowOut{}, err
	}
	if _, err := o.env.Delete(ctx, engine.Ref{Store: l.Src, Table: schema.TableVars, ID: l.Row.ID}); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: l.Row.ID.String(), Store: string(l.Src),
		Human: fmt.Sprintf("✓ removed variable %s (%s) from %s\n", text(l.Row, vName), short(l.Row.ID), l.Src)}, nil
}

func (o ops) varsWhere(ctx context.Context, in VarsShowIn) (VarsOut, error) {
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableVars)
	if err != nil {
		return VarsOut{}, err
	}
	out := VarsOut{Data: []Var{}}
	for _, l := range rows {
		if text(l.Row, vName) == in.Name || l.Row.ID.String() == in.Name || slices.Contains(texts(l.Row, vAliases), in.Name) {
			v := varOf(l)
			v.Hidden = len(out.Data) > 0
			out.Data = append(out.Data, v)
		}
	}
	if len(out.Data) == 0 {
		return VarsOut{}, errs.New(errs.NotFound, "no variable "+quote(in.Name))
	}
	out.Count = len(out.Data)
	return out, nil
}
