package settings

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
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
	// --created-by / --updated-by: the shared writer filters of every
	// list (INTEG-AUDIT-1; engine.List applies them to the table that
	// declares this list's noun, schema.Noun).
	metalist.Audit
}

// VarsShowIn is the input of vars.show.
type VarsShowIn struct {
	Name string `json:"id" arg:"0" desc:"the variable (name or var_ id)"`
	To   string `json:"to,omitempty" flag:"to" desc:"the definition in this place"`
}

// VarsSetIn is the input of vars.set.
type VarsSetIn struct {
	Name        string `json:"name" arg:"0" desc:"the variable name (snake_case)"`
	Value       string `json:"value,omitempty" arg:"1" desc:"the value (or --set-stdin)"`
	To          string `json:"to,omitempty" flag:"to" desc:"where the variable lives (repo, me, <store>, <store>/<folder>; default repo)"`
	Shareable   bool   `json:"shareable,omitempty" flag:"shareable" desc:"resolves for everyone who reads this repo"`
	Type        string `json:"type,omitempty" flag:"type" desc:"text | number | url | dns | ip | cidr | email | date | datetime | duration | secret_ref (default text)"`
	Description string `json:"description,omitempty" flag:"description" desc:"what the variable is"`
	// SetStdin reads the value from stdin (a long value, or one that should
	// not sit in shell history; SECRETS_AND_REFERENCES 11.1).
	SetStdin bool `json:"set_stdin,omitempty" flag:"set-stdin" desc:"read the value from stdin (one trailing newline dropped)"`
	// AllowDangling keeps a value whose ${var:…} names no variable yet
	// (2.8: E_REF_DANGLING otherwise).
	AllowDangling bool `json:"allow_dangling,omitempty" flag:"allow-dangling" desc:"accept a ${var:…} reference to a variable that does not exist yet"`
}

// VarsRenameIn is the input of vars.rename.
type VarsRenameIn struct {
	Name string `json:"id" arg:"0" desc:"the variable (name or var_ id)"`
	New  string `json:"new_name" arg:"1" desc:"the new name (the old one stays an alias)"`
	To   string `json:"to,omitempty" flag:"to" desc:"the definition in this place"`
}

// VarsRmIn is the input of vars.rm.
type VarsRmIn struct {
	Name   string `json:"id" arg:"0" desc:"the variable (name or var_ id)"`
	To     string `json:"to,omitempty" flag:"to" desc:"the definition in this place"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
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
	// CreatedAt / UpdatedAt / CreatedBy / UpdatedBy are the row
	// envelope's writers (INTEG-AUDIT-1/2, auditrec), last so the older
	// keys keep their order.
	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	CreatedBy string    `json:"created_by,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
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
	return s + auditrec.LineAt(v.CreatedAt, v.CreatedBy, v.UpdatedAt, v.UpdatedBy) // INTEG-AUDIT-2
}

func (o ops) varOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpVarsList, Summary: "List project variables (nearest definition per name)", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors, Examples: []op.Example{{Title: "every variable this repo sees", CLI: "lore vars list"}}}, o.varsList),
		op.Define(op.Spec{Name: OpVarsShow, Summary: "Show one variable", Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors, Examples: []op.Example{{Title: "one variable", CLI: "lore vars show --id db_host"}}}, o.varsShow),
		op.Define(op.Spec{Name: OpVarsSet, Summary: "Set a variable (adds it, or changes its value in that place)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.RefDangling, errs.RefCycle), Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "a dns variable", CLI: "lore vars set --name db_host --value db.example.com --type dns", Want: "var_"}}}, o.varsSet),
		op.Define(op.Spec{Name: OpVarsRename, Summary: "Rename a variable (the old name stays an alias)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Examples: []op.Example{{Title: "rename a variable (the old name stays an alias)", CLI: "lore vars rename --id db_host --new-name database_host"}}}, o.varsRename),
		op.Define(op.Spec{Name: OpVarsRm, Summary: "Remove a variable (to the trash, restorable)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Confirm: op.ConfirmRule{MayAsk: true}, Examples: []op.Example{{Title: "remove a variable", CLI: "lore vars rm --id db_host"}, {Title: "without a terminal", CLI: "lore vars rm --id db_host --yes"}}}, o.varsRm),
		op.Define(op.Spec{Name: OpVarsWhere, Summary: "Every place that defines a variable", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors, Examples: []op.Example{{Title: "the rows that use a variable", CLI: "lore vars where --id db_host"}}}, o.varsWhere),
	}
}

func varOf(l located) Var {
	return Var{ID: l.Row.ID.String(), Name: text(l.Row, vName), Type: text(l.Row, vType), Value: display(fromJSON(l.Row.Fields[vValue])),
		Store: string(l.Src), Shareable: flag(l.Row, vShareable), Aliases: texts(l.Row, vAliases), Description: text(l.Row, vDescription), CreatedAt: l.Row.CreatedAt, UpdatedAt: l.Row.UpdatedAt, CreatedBy: auditrec.Of(l.Row).CreatedBy, UpdatedBy: auditrec.Of(l.Row).UpdatedBy}
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
	if terr := inTrashByKey(ctx, o.env, sources(o.env, to), schema.TableVars, func(l located) bool {
		return l.Row.ID.String() == key || text(l.Row, vName) == key || slices.Contains(texts(l.Row, vAliases), key)
	}); terr != nil {
		return located{}, terr // DELETION 3.2: E_IN_TRASH for a trashed variable
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

// varsSet adds or changes a variable:
//
//	name check ─▶ value (arg or --set-stdin) ─▶ references valid (E_REF_SYNTAX,
//	5.1/8.1) ─▶ every ${var:…} defined somewhere seen (E_REF_DANGLING unless
//	--allow-dangling, 2.8) ─▶ type parse ─▶ write; the cache then refreshes
//	every row citing it (SQ3, wire/resolver.go).
func (o ops) varsSet(ctx context.Context, in VarsSetIn) (RowOut, error) {
	if !snakeName.MatchString(in.Name) {
		return RowOut{}, errs.New(errs.InvalidInput, "variable name "+quote(in.Name)+" is not snake_case").WithHint(snakeHint(in.Name))
	}
	if in.SetStdin {
		if in.Value != "" {
			return RowOut{}, errs.New(errs.InvalidInput, "give the value as an argument or with --set-stdin, not both")
		}
		v, err := readStdinValue()
		if err != nil {
			return RowOut{}, err
		}
		in.Value = v
	}
	if err := refs.Validate(in.Value); err != nil {
		return RowOut{}, err
	}
	// SEC 8.3 / CLI_REFERENCE vars set: a value that would close a loop of
	// variables is refused now, not at the first expansion (always, also
	// with --allow-dangling: a dangling name cannot close a loop)
	if err := o.checkVarCycle(ctx, in.Name, in.Value); err != nil {
		return RowOut{}, err
	}
	if !in.AllowDangling {
		if err := o.checkVarRefs(ctx, in.Name, in.Value); err != nil {
			return RowOut{}, err
		}
	}
	target := targetOf(in.To)
	rows, err := liveRows(ctx, o.env, sources(o.env, string(target)), schema.TableVars)
	if err != nil {
		return RowOut{}, err
	}
	var existing *located
	if hit := where(rows, map[string]string{vName: in.Name}); len(hit) > 0 {
		existing = &hit[0]
	}
	// Changing the value of a variable in its place keeps what the caller
	// did not name: its type (the new value is checked against it) and
	// its shareable flag (switching shareable is `lore move`, same id,
	// CONFIG_GUIDE VARS ARE DATA). Only a new variable defaults to text,
	// not shareable.
	typ, shareable := in.Type, in.Shareable
	if existing != nil {
		if typ == "" {
			typ = text(existing.Row, vType)
		}
		shareable = shareable || flag(existing.Row, vShareable)
	}
	if typ == "" {
		typ = string(value.KindText)
	}
	if !slices.Contains(varTypes(), typ) {
		return RowOut{}, errs.New(errs.InvalidInput, "--type "+quote(typ)+" is not one of "+strings.Join(varTypes(), ", "))
	}
	kind := value.Kind(typ)
	if typ == varTypeSecretRef || len(refs.References(in.Value)) > 0 {
		// a value holding references is checked as its type after
		// expansion (11.5), by lore lint; here it is stored as typed text.
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
	fields := map[string]value.Value{vValue: jv}
	if existing == nil || in.Shareable {
		fields[vShareable] = value.BoolOf(shareable)
	}
	if err = optTexts(fields, map[string]string{vName: in.Name, vType: typ, vDescription: in.Description}); err != nil {
		return RowOut{}, err
	}
	if existing != nil {
		delete(fields, vName)
		w, uerr := o.env.Update(ctx, engine.Ref{Store: target, Table: schema.TableVars, ID: existing.Row.ID}, fields)
		if uerr != nil {
			return RowOut{}, uerr
		}
		return RowOut{ID: w.Row.ID.String(), Store: string(target),
			Human: fmt.Sprintf("Changed %s %s = %s (%s) %s.\n", short(w.Row.ID), in.Name, storedValue(w.Row), typ, varWhere(target, shareable))}, nil
	}
	w, err := create(ctx, o.env, target, schema.TableVars, map[string]string{vName: in.Name}, fields)
	if err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(target),
		Human: fmt.Sprintf("Added %s %s = %s (%s) %s.\n", short(w.Row.ID), in.Name, storedValue(w.Row), typ, varWhere(target, shareable))}, nil
}

// storedValue is the value a vars set wrote, as show prints it. Why not
// the typed input: an answer to the secret question (--secret-ref,
// --secret-unstored) replaced the detected secret, and echoing the input
// printed that secret in clear to the terminal and to agent logs.
func storedValue(r row.Row) string { return display(fromJSON(r.Fields[vValue])) }

// varWhere is the "where and who sees it" tail of a vars set line.
func varWhere(target store.Source, shareable bool) string {
	if shareable {
		return "in " + string(target) + " (shareable: resolves for everyone with access)"
	}
	return "in " + string(target) + " (not shareable: clients see ${var:<name>})"
}

func (o ops) varsRename(ctx context.Context, in VarsRenameIn) (RowOut, error) {
	if !snakeName.MatchString(in.New) {
		return RowOut{}, errs.New(errs.InvalidInput, "variable name "+quote(in.New)+" is not snake_case").WithHint(snakeHint(in.New))
	}
	l, err := o.varByName(ctx, in.Name, in.To)
	if err != nil {
		return RowOut{}, err
	}
	if hit := where([]located{l}, map[string]string{vName: in.New}); len(hit) > 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "the variable is already named "+in.New)
	}
	if other, oerr := o.varByName(ctx, in.New, string(l.Src)); oerr == nil && other.Row.ID != l.Row.ID {
		return RowOut{}, errs.New(errs.NameTaken, "a variable named "+quote(in.New)+" already exists in "+string(l.Src))
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
	return o.patchRow(ctx, l, map[string]value.Value{vName: nv, vAliases: al}, "renamed variable "+old+" → "+in.New+" (old name kept as an alias)")
}

func (o ops) varsRm(ctx context.Context, in VarsRmIn) (RowOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // INTEG-TRASH-3; E_REASON_REQUIRED under delete.require_reason
	l, err := o.varByName(ctx, in.Name, in.To)
	if err != nil {
		return RowOut{}, err
	}
	// One confirm rule: every delete asks unless --yes, after the lookup
	// (a missing variable is reported, not asked about).
	if err := bulk.Ask(ctx, "remove variable "+text(l.Row, vName)+" ("+short(l.Row.ID)+") from "+string(l.Src)+"? It goes to the trash; text naming it keeps ${var:"+text(l.Row, vName)+"} as written"); err != nil {
		return RowOut{}, err
	}
	if _, err := o.env.Delete(ctx, engine.Ref{Store: l.Src, Table: schema.TableVars, ID: l.Row.ID}); err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: l.Row.ID.String(), Store: string(l.Src),
		Human: fmt.Sprintf("✓ removed variable %s (%s) from %s\n", text(l.Row, vName), short(l.Row.ID), l.Src)}, nil
}

func (o ops) varsWhere(ctx context.Context, in VarsShowIn) (VarsOut, error) {
	rows, err := liveRows(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableVars)
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
		if terr := inTrashByKey(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableVars, func(l located) bool {
			return text(l.Row, vName) == in.Name || l.Row.ID.String() == in.Name || slices.Contains(texts(l.Row, vAliases), in.Name)
		}); terr != nil {
			return VarsOut{}, terr // DELETION 3.2
		}
		return VarsOut{}, errs.New(errs.NotFound, "no variable "+quote(in.Name))
	}
	out.Count = len(out.Data)
	return out, nil
}

// maxStdinValue caps a --set-stdin value (the expanded-text cap, D17).
const maxStdinValue = refs.MaxExpanded

// readStdinValue reads a variable value from stdin, dropping one trailing
// newline (what `echo` and a heredoc add).
func readStdinValue() (string, error) {
	b, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinValue+1))
	if err != nil {
		return "", errs.Wrap(errs.InvalidInput, "vars set: read stdin", err)
	}
	if len(b) > maxStdinValue {
		return "", errs.New(errs.InvalidInput, "vars set: the stdin value is over 64 KB")
	}
	s := strings.TrimSuffix(string(b), "\n")
	return strings.TrimSuffix(s, "\r"), nil
}

// checkVarRefs refuses a value citing a variable no store this repo sees
// defines (2.8). A variable citing itself is a cycle, refused the same way.
// varRefNames are the ${var:…} names a value refers to directly.
func varRefNames(val string) []string {
	var names []string
	for _, r := range refs.References(val) {
		if r.Scheme == refs.SchemeVar && r.Modifier == refs.ModNone {
			names = append(names, r.Target)
		}
	}
	return names
}

// checkVarCycle refuses E_REF_CYCLE when self's new value val reaches self
// again through the variables every attached store holds (by name, id or
// alias: an alias and its name count as one, SEC 8.3); the message prints
// the chain (var:a -> var:b -> var:a).
func (o ops) checkVarCycle(ctx context.Context, self, val string) error {
	if len(varRefNames(val)) == 0 {
		return nil
	}
	rows, err := liveRows(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableVars)
	if err != nil {
		return err
	}
	canon := map[string]string{} // name, id or alias -> name
	values := map[string]string{}
	for _, l := range rows {
		n := text(l.Row, vName)
		canon[n], canon[l.Row.ID.String()] = n, n
		for _, a := range texts(l.Row, vAliases) {
			canon[a] = n
		}
		if _, seen := values[n]; !seen { // nearest store first
			values[n] = display(fromJSON(l.Row.Fields[vValue]))
		}
	}
	if c, ok := canon[self]; ok {
		self = c
	}
	canon[self] = self
	values[self] = val
	var walk func(n string, chain []string) []string
	visited := map[string]bool{}
	walk = func(n string, chain []string) []string {
		for _, r := range varRefNames(values[n]) {
			next, ok := canon[r]
			if !ok {
				continue // dangling: checkVarRefs' question
			}
			if next == self {
				return append(chain, next)
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			if got := walk(next, append(chain, next)); got != nil {
				return got
			}
		}
		return nil
	}
	if chain := walk(self, []string{self}); chain != nil {
		for i, n := range chain {
			chain[i] = "var:" + n
		}
		return errs.New(errs.RefCycle, "variables refer to each other in a loop: "+strings.Join(chain, " -> ")).
			WithHint("break the chain: give one of them a plain value; nothing written")
	}
	return nil
}

func (o ops) checkVarRefs(ctx context.Context, self, val string) error {
	var names []string
	for _, r := range refs.References(val) {
		if r.Scheme == refs.SchemeVar && r.Modifier == refs.ModNone {
			names = append(names, r.Target)
		}
	}
	if len(names) == 0 {
		return nil
	}
	rows, err := liveRows(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableVars)
	if err != nil {
		return err
	}
	for _, n := range names {
		found := n != self && slices.ContainsFunc(rows, func(l located) bool {
			return text(l.Row, vName) == n || l.Row.ID.String() == n || slices.Contains(texts(l.Row, vAliases), n)
		})
		if !found {
			return errs.New(errs.RefDangling, self+" refers to ${var:"+n+"}, which is not defined in this repo or any store it sees").
				WithHint("lore vars set --name " + n + " --value <value> first, or pass --allow-dangling")
		}
	}
	return nil
}

// snakeHint suggests the snake_case form of a refused variable name
// (INTEG-QUESTIONS-10): "Bad-Name" → "use a snake_case name such as
// bad_name". With nothing usable left it names the shape instead.
func snakeHint(name string) string {
	if s := meta.ToSnake(name); s != "" {
		return "use a snake_case name such as " + s
	}
	return "use a snake_case name: a-z, 0-9 and _, starting with a letter"
}
