package engine

// Metadata on row writes (METADATA_DESIGN 3.2, 3.6, 3.9, 3.13; R13, MQ12):
//
//	ops ─► WithMeta(ctx, MetaWrite) ─► Create / Update
//	   create/Update ─► applyMeta(target, def, row id, current meta, mw)
//	      ├─ table accepts meta? (U19)            E_META_TABLE
//	      ├─ metaRegistry: key rows of every store in precedence order
//	      ├─ per term: name rules / reserved      E_META_KEY, _RESERVED, _DEPTH
//	      │           resolve: id │ scope │ nearest; 2+ defs E_META_AMBIGUOUS; none → new key
//	      │           coerce per registered type  E_META_TYPE, _VALUE, _SHAPE
//	      │           3.19 rules: pattern, min/max, deprecated (warn; strict refuses E_META_VALUE)
//	      ├─ unset (parent removes children)      E_META_UNKNOWN
//	      ├─ create only (createMeta): defaults, then required_on   E_META_REQUIRED
//	      ├─ size vs meta.max_bytes, 4 KB warning E_META_SIZE
//	      ├─ secret scan of the values            E_SECRET_DETECTED
//	      └─ register new keys (internal in a store, R2-Q2; added_by/reviewed, MQ7)
//	   the row is written with the new meta in the same put

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// WarnMeta is a metadata note: a "stored as" line, a new key, a tag-like
// name, a non-strict type mismatch, the 4 KB warning.
const WarnMeta WarnCode = "W_META"

// MetaWrite is a metadata change riding on one row write. Set terms carry
// command-line text, JSON terms carry JSON text (a file or stdin already
// read by the caller), Unset names keys (a parent removes its fields).
type MetaWrite struct {
	Set, JSON []meta.Term
	Unset     []string
	// Table and ID bind the change to one row: a create of Table (ID zero),
	// or the update of ID. Any other write in the same call ignores it.
	Table row.Table
	ID    ids.AnyID
	// prefer is the op's own table for a change bound to neither (Run
	// sets it from the op's noun): a side write of another table, such
	// as task add's auto-created tasklist, never claims it (OV10-BIND).
	prefer row.Table
	used   *atomic.Bool
}

// Empty reports a change with nothing to do.
func (m MetaWrite) Empty() bool { return len(m.Set) == 0 && len(m.JSON) == 0 && len(m.Unset) == 0 }

// metaKey keys a MetaWrite in a context.
type metaKey struct{}

// WithMeta returns ctx carrying mw for the one write it is bound to. The
// row write API keeps its signature, so every noun and Env gains metadata
// through the shared ops layer without a new method (R13: one flag
// helper for every entity).
func WithMeta(ctx context.Context, mw MetaWrite) context.Context {
	if mw.Empty() {
		return ctx
	}
	mw.used = &atomic.Bool{}
	return context.WithValue(ctx, metaKey{}, mw)
}

// MetaCarry is the metadata a --supersedes add copies from the replaced
// row (METADATA 3.19) when the add also has its own --meta flags: on the
// create of Table, after the flags are applied and BEFORE the create-only
// rules, every carried key the new row does not hold yet is set. So a
// required key set only on the old row is satisfied by the copy instead
// of failing E_META_REQUIRED before it (OV30-CARRY-REQ).
type MetaCarry struct {
	// Terms name their key by id (mky_…=<json>).
	Terms  []meta.Term
	Table  row.Table
	used   *atomic.Bool
	filled *atomic.Int32
}

// Filled is how many carried keys the create set (0 before it ran).
func (c MetaCarry) Filled() int {
	if c.filled == nil {
		return 0
	}
	return int(c.filled.Load())
}

// metaCarryKey keys a MetaCarry in a context.
type metaCarryKey struct{}

// WithMetaCarry returns ctx carrying c for the next create of c.Table and
// the bound MetaCarry, whose Filled reports the result after the create.
func WithMetaCarry(ctx context.Context, c MetaCarry) (context.Context, MetaCarry) {
	if len(c.Terms) == 0 {
		return ctx, c
	}
	c.used, c.filled = &atomic.Bool{}, &atomic.Int32{}
	return context.WithValue(ctx, metaCarryKey{}, c), c
}

// carryFor claims the carry bound to a create of table.
func carryFor(ctx context.Context, table row.Table) (MetaCarry, bool) {
	c, ok := ctx.Value(metaCarryKey{}).(MetaCarry)
	if !ok || c.Table != table || !c.used.CompareAndSwap(false, true) {
		return MetaCarry{}, false
	}
	return c, true
}

// metaFor returns the change bound to a create of table (id zero) or an
// update of id, claiming it so it applies once. A change bound to neither
// (the --meta flags of a command, bound by Run) takes the command's first
// write of a table that accepts metadata: the add's new row, the edit's row.
func metaFor(ctx context.Context, table row.Table, id ids.AnyID) (MetaWrite, bool) {
	mw, ok := ctx.Value(metaKey{}).(MetaWrite)
	if !ok {
		return MetaWrite{}, false
	}
	first := mw.Table == "" && mw.ID.IsZero() && metaTables[table] && (mw.prefer == "" || mw.prefer == table)
	match := first || (id.IsZero() && mw.ID.IsZero() && mw.Table == table) || (!id.IsZero() && mw.ID == id)
	if !match || !mw.used.CompareAndSwap(false, true) {
		return MetaWrite{}, false
	}
	return mw, true
}

// MetaBound reports that ctx carries a --meta change no write has taken
// yet: an edit whose only flags are --meta is a real change (OV10), so a
// noun's "nothing to change" check asks this before refusing and then
// writes an empty patch the change rides on.
func MetaBound(ctx context.Context) bool {
	mw, ok := ctx.Value(metaKey{}).(MetaWrite)
	return ok && mw.used != nil && !mw.used.Load()
}

// metaTables are the tables whose rows accept metadata from the CLI (U19);
// every other synced table answers E_META_TABLE.
var metaTables = map[row.Table]bool{
	schema.TableArchitectureNotes: true, schema.TableBehaviours: true, schema.TableComments: true,
	schema.TableCookbookRecipes: true, schema.TableDecisions: true, schema.TableExternalSources: true,
	schema.TableHandoffs: true, schema.TableHotfixes: true, schema.TableIncidents: true, schema.TableMemories: true,
	schema.TableMissions: true, schema.TablePatterns: true, schema.TablePlans: true, schema.TablePlaybooks: true,
	schema.TablePrompts: true, schema.TableReminders: true, schema.TableRules: true, schema.TableSnapshots: true,
	schema.TableSuggestions: true, schema.TableTasklists: true, schema.TableTasks: true, schema.TableTastePrefs: true,
	schema.TableTechDocs: true, schema.TableWorkflows: true, schema.TableWorkspaces: true, schema.TableResources: true,
}

// AcceptsMeta reports whether rows of table take metadata from the CLI.
func AcceptsMeta(table row.Table) bool { return metaTables[table] }

// meta_keys / meta_values fields read here (schema.Builtin).
const (
	mkName, mkScope, mkType            = "name", "scope", "type"
	mkStrict, mkInternal, mkSensitive  = "strict", "internal", "sensitive"
	mkAddedBy, mkReviewed, mvKey, mvLb = "added_by", "reviewed", "key_id", "label"
	// METADATA 3.19 everyday-feature fields and the 3.12 fold.
	mkPattern, mkMin, mkMax, mkDefault  = "pattern", "min", "max", "default"
	mkRequiredOn, mkDefaultOn           = "required_on", "default_on"
	mkDeprecated, mkReplacedBy, mkMerge = "deprecated", "replaced_by", "merged_into"
	// scope labels (ops/settings uses the same words).
	scopeRepo, scopeStore = "repo", "store"
	folderSep             = "/"
)

// keyDef is one visible key definition.
type keyDef struct {
	id                          ids.ID[ids.MetaKey]
	name, scope                 string
	typ                         meta.Type
	strict, sensitive, internal bool
	src                         store.Source
	values                      []valueDef
	// rules are the 3.19 checks (pattern, min/max, deprecation); requiredOn
	// and defaultOn name tables, dflt is the default's command-line text.
	rules                 meta.Rules
	requiredOn, defaultOn []string
	dflt                  string
	// retired: merged_into is set (3.12 fold); a bare name never resolves
	// to a retired key, its survivor holds the name's values now.
	retired bool
}

// valueDef is one allowed value of a key.
type valueDef struct {
	id          ids.AnyID
	name, label string
}

// label is the "scope" shown with a definition.
func (d keyDef) label() string { return d.scope + " (" + string(d.src) + ")" }

// metaScope is the scope label of a key registered in src.
func metaScope(src store.Source) string {
	s := string(src)
	switch {
	case src == "" || src == store.SourceRepo:
		return scopeRepo
	case strings.Contains(s, folderSep):
		_, f, _ := strings.Cut(s, folderSep)
		return f
	}
	return scopeStore
}

// metaRegistry reads every attached store's key rows, in precedence order.
func (e *Engine) metaRegistry(ctx context.Context) ([]keyDef, error) {
	var out []keyDef
	for _, src := range e.Stores() {
		keys, err := e.List(ctx, src, schema.TableMetaKeys)
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 {
			continue
		}
		vals, err := e.List(ctx, src, schema.TableMetaValues)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			mk, err := ids.Parse[ids.MetaKey](k.ID.String())
			if err != nil {
				continue // a key row with a foreign id is lint's to report
			}
			t, err := meta.ParseType(fieldText(k, mkType))
			if err != nil {
				continue
			}
			d := keyDef{id: mk, name: fieldText(k, mkName), scope: fieldText(k, mkScope), typ: t, src: src,
				strict: fieldBool(k, mkStrict), sensitive: fieldBool(k, mkSensitive), internal: fieldBool(k, mkInternal),
				rules: meta.Rules{Pattern: fieldText(k, mkPattern), Min: fieldText(k, mkMin), Max: fieldText(k, mkMax),
					Deprecated: fieldBool(k, mkDeprecated), ReplacedBy: fieldText(k, mkReplacedBy)},
				requiredOn: fieldList(k, mkRequiredOn), defaultOn: fieldList(k, mkDefaultOn), dflt: fieldText(k, mkDefault),
				retired: fieldText(k, mkMerge) != ""}
			for _, v := range vals {
				if fieldText(v, mvKey) == k.ID.String() {
					d.values = append(d.values, valueDef{id: v.ID, name: fieldText(v, mkName), label: fieldText(v, mvLb)})
				}
			}
			out = append(out, d)
		}
	}
	return out, nil
}

// fieldText is a text field of r ("" when absent).
func fieldText(r row.Row, f string) string {
	if v, ok := r.Fields[f]; ok && v != nil && v.Kind() != value.KindNull {
		return v.String()
	}
	return ""
}

// fieldList is a JSON list-of-text field of r (nil when absent; the
// store reads a JSON array back as a list).
func fieldList(r row.Row, f string) []string { return value.ItemStrings(r.Fields[f]) }

// fieldBool is a bool field of r (false when absent).
func fieldBool(r row.Row, f string) bool {
	b, ok := r.Fields[f].(value.Bool)
	return ok && b.Bool()
}

// pendingKey is a key a write registers.
type pendingKey struct {
	id   ids.ID[ids.MetaKey]
	name string
	home store.Source
	typ  meta.Type
}

// metaPlan is applyMeta's working state.
type metaPlan struct {
	e        *Engine
	target   store.Source
	reg      []keyDef
	entries  map[ids.ID[ids.MetaKey]]meta.Entry
	pending  []pendingKey
	warnings []Warning
	columns  map[string]bool
	changed  []ids.ID[ids.MetaKey]
	// ask is the terminal's yes/no question for an interactive caller
	// (nil otherwise): an ambiguous bare name asks which definition to
	// use instead of failing (OV10, P2-META-8).
	ask func(question string) (bool, error)
}

// applyMeta returns cur changed by mw (see the file's flow sketch). A
// write with no change returns cur untouched.
func (e *Engine) applyMeta(ctx context.Context, cl call, target store.Source, def schema.TableDef, rowID ids.AnyID,
	cur map[ids.ID[ids.MetaKey]]value.JSON, mw MetaWrite) (map[ids.ID[ids.MetaKey]]value.JSON, []Warning, error) {
	return e.applyMetaOn(ctx, cl, target, def, rowID, cur, mw, false)
}

// createMeta is the metadata step of every create (write.go): the change
// riding on it (has) plus, on a table that accepts metadata, the 3.19
// create-only rules: defaults (default_on, printed as "default: k = v",
// never back-filled) and required keys (required_on, E_META_REQUIRED).
// A create with no change on a table without metadata does nothing.
func (e *Engine) createMeta(ctx context.Context, cl call, target store.Source, def schema.TableDef, rowID ids.AnyID,
	mw MetaWrite, has bool) (map[ids.ID[ids.MetaKey]]value.JSON, []Warning, error) {
	if !has && !metaTables[def.Name] {
		return nil, nil, nil
	}
	return e.applyMetaOn(ctx, cl, target, def, rowID, nil, mw, true)
}

// applyMetaOn is applyMeta; creating adds the create-only rules (3.19).
func (e *Engine) applyMetaOn(ctx context.Context, cl call, target store.Source, def schema.TableDef, rowID ids.AnyID,
	cur map[ids.ID[ids.MetaKey]]value.JSON, mw MetaWrite, creating bool) (map[ids.ID[ids.MetaKey]]value.JSON, []Warning, error) {
	if !metaTables[def.Name] {
		return nil, nil, errs.New(errs.MetaTable, "metadata cannot be set on "+string(def.Name)+" from the command line; knowledge, work and resource rows accept it").
			WithHint("lore help meta")
	}
	reg, err := e.metaRegistry(ctx)
	if err != nil {
		return nil, nil, err
	}
	p := &metaPlan{e: e, target: target, reg: reg, entries: map[ids.ID[ids.MetaKey]]meta.Entry{}, columns: e.columnNames()}
	if cl.caller.Interactive && !cl.caller.Yes && e.confirmer != nil {
		p.ask = func(q string) (bool, error) { return e.confirmer.Confirm(ctx, q) }
	}
	for id, j := range cur {
		en, derr := meta.DecodeEntry(j)
		if derr != nil {
			return nil, nil, derr
		}
		p.entries[id] = en
	}
	for _, t := range mw.Set {
		if err := p.set(t, false); err != nil {
			return nil, nil, err
		}
	}
	for _, t := range mw.JSON {
		if err := p.set(t, true); err != nil {
			return nil, nil, err
		}
	}
	for _, name := range mw.Unset {
		if err := p.unset(rowID, name); err != nil {
			return nil, nil, err
		}
	}
	if creating {
		// OV30-CARRY-REQ: carried keys fill in before the required check.
		if c, ok := carryFor(ctx, def.Name); ok {
			for _, t := range c.Terms {
				if _, has := p.entries[t.KeyID]; has {
					continue // the add's own --meta wins
				}
				if err := p.set(t, true); err != nil {
					return nil, nil, err
				}
				c.filled.Add(1)
			}
		}
		if err := p.createRules(string(def.Name)); err != nil {
			return nil, nil, err
		}
	}
	if err := p.checkSize(); err != nil {
		return nil, nil, err
	}
	if err := e.scanMeta(cl, target, rowID, p); err != nil {
		return nil, nil, err
	}
	if err := p.register(ctx, cl); err != nil {
		return nil, nil, err
	}
	out := make(map[ids.ID[ids.MetaKey]]value.JSON, len(p.entries))
	for id, en := range p.entries {
		j, eerr := en.Encode()
		if eerr != nil {
			return nil, nil, eerr
		}
		out[id] = j
	}
	if len(out) == 0 {
		out = nil
	}
	return out, p.warnings, nil
}

// columnNames is every field name of every table (reserved as key names).
func (e *Engine) columnNames() map[string]bool {
	out := map[string]bool{}
	for _, t := range e.reg.Tables() {
		for _, f := range t.Fields {
			out[f.Name] = true
		}
	}
	for _, f := range []string{row.KeyCreatedAt, row.KeyCreatedBy, row.KeyUpdatedAt, row.KeyUpdatedBy, "deleted_at", "deleted_by"} {
		out[f] = true
	}
	return out
}

// warn adds a metadata note.
func (p *metaPlan) warn(field, msg string) {
	p.warnings = append(p.warnings, Warning{Code: WarnMeta, Field: field, Message: msg})
}

// set applies one write term.
func (p *metaPlan) set(t meta.Term, isJSON bool) error {
	d, fields, err := p.resolve(t)
	if err != nil {
		return err
	}
	var name string
	var typ meta.Type
	switch {
	case d != nil:
		name, typ = d.name, d.typ
		if !t.Type.IsZero() && len(fields) == 0 && t.Type != d.typ {
			return errs.New(errs.MetaType, fmt.Sprintf("meta: %s is registered as %s, not %s", d.name, d.typ, t.Type)).
				WithHint("drop the :type, or lore meta edit " + d.name + " --type " + t.Type.String())
		}
	default:
		name = meta.TopSegment(t.Key)
	}
	// the value's own type: a field of an object takes its written type
	vt := typ
	if len(fields) > 0 || d == nil {
		vt = t.Type
	}
	if vt.IsZero() && isJSON {
		if vt, err = meta.InferType([]byte(t.Value)); err != nil {
			return err
		}
	}
	if vt.IsZero() {
		vt = meta.Type{Kind: value.KindText}
	}
	var c meta.Coerced
	if isJSON {
		c, err = meta.CoerceJSON(vt, []byte(t.Value))
	} else {
		c, err = meta.Coerce(vt, t.Value)
	}
	if err != nil && errs.Has(err, errs.MetaType) && d != nil && !d.strict && t.Type.IsZero() && !isJSON {
		// a non-strict key warns and keeps the text (meta add --strict)
		c, err = meta.Coerce(meta.Type{Kind: value.KindText}, t.Value)
		if err == nil {
			p.warn(name, name+": value does not parse as "+d.typ.String()+"; stored as text (the key is not strict)")
		}
	}
	if err != nil {
		return err
	}
	var vname []byte
	if d != nil && len(d.values) > 0 && len(fields) == 0 {
		if c.Raw, vname, err = allowed(*d, c.Raw); err != nil {
			return err
		}
	}
	if d != nil && len(fields) == 0 {
		if err = p.checkRules(*d, c.Raw); err != nil {
			return err
		}
	}
	id := p.keyID(d, t, name, fields, vt)
	en := p.entries[id]
	v := c.Raw
	if len(fields) > 0 {
		if v, err = meta.SetPath(en.V, fields, c.Raw); err != nil {
			return err
		}
	}
	en.V, en.VName = v, vname
	if d != nil {
		en.Name, en.Scope, en.Strict, en.Sensitive = d.name, d.scope, d.strict, d.sensitive
	} else {
		home := p.home(t)
		en.Name, en.Scope = name, metaScope(home)
	}
	p.entries[id] = en
	p.changed = append(p.changed, id)
	shown := strings.Join(append([]string{name}, fields...), ".")
	for _, n := range c.Notes {
		if en.Sensitive {
			n = "normalised (value hidden)"
		}
		p.warn(shown, shown+": "+n)
	}
	if meta.TagLike(name) {
		p.warn(shown, "key "+quoteName(name)+" looks like a tag; lore keeps labels in tags (lore tag), not in metadata")
	}
	return nil
}

// checkRules applies a key's 3.19 rules to one coerced value: a pattern or
// min/max violation and a write of a deprecated key warn on a normal key
// and are refused on a strict key (E_META_VALUE).
func (p *metaPlan) checkRules(d keyDef, raw []byte) error {
	if d.rules.Empty() {
		return nil
	}
	msgs := []string{}
	why, err := meta.Violation(d.name, d.typ, raw, d.rules)
	if err != nil {
		return err
	}
	if why != "" {
		msgs = append(msgs, why)
	}
	if n := meta.DeprecatedNote(d.name, d.rules); n != "" {
		msgs = append(msgs, n)
	}
	for _, m := range msgs {
		if d.strict {
			return errs.New(errs.MetaValue, "meta: "+m+" (the key is strict)").WithHint("lore meta keys --key " + d.name + " shows its rules")
		}
		p.warn(d.name, m)
	}
	return nil
}

// createRules applies defaults then required keys on a create (3.19): a
// key with default_on naming the table and no value yet gets its default
// (through the normal set path, so every check runs); then every key whose
// required_on names the table must have a value, or E_META_REQUIRED.
func (p *metaPlan) createRules(table string) error {
	have := func(d keyDef) bool { _, ok := p.entries[d.id]; return ok }
	for _, d := range p.reg {
		if d.retired || d.dflt == "" || !slices.Contains(d.defaultOn, table) || have(d) {
			continue
		}
		if err := p.set(meta.Term{KeyID: d.id, Value: d.dflt}, false); err != nil {
			return err
		}
		p.warn(d.name, "default: "+d.name+" = "+d.dflt)
	}
	required := map[string][]string{}
	var names []string
	for _, d := range p.reg {
		if d.retired || len(d.requiredOn) == 0 {
			continue
		}
		if have(d) {
			names = append(names, d.name)
		}
		required[d.name] = d.requiredOn
	}
	if miss := meta.MissingRequired(table, required, names); len(miss) > 0 {
		return errs.New(errs.MetaRequired, table+" here need: "+strings.Join(miss, ", ")+" (required_on: "+table+")").
			WithHint("add --meta " + miss[0] + "=<value>")
	}
	return nil
}

// quoteName quotes a key name in a message.
func quoteName(s string) string { return `"` + s + `"` }

// resolve finds the definition a term names (nil = a new key) and the
// field path inside it (MQ12: id, else scope, else the one visible
// definition; two or more → E_META_AMBIGUOUS).
func (p *metaPlan) resolve(t meta.Term) (*keyDef, []string, error) {
	if !t.KeyID.IsZero() {
		for i := range p.reg {
			if p.reg[i].id == t.KeyID {
				return &p.reg[i], t.Fields, nil
			}
		}
		return nil, nil, errs.New(errs.MetaUnregistered, "meta: no key "+t.KeyID.String()+" is visible here").
			WithHint("lore sync, or lore meta keys --ids")
	}
	depth, err := p.maxDepth()
	if err != nil {
		return nil, nil, err
	}
	if err := meta.CheckName(t.Key, depth); err != nil {
		return nil, nil, err
	}
	if err := meta.Reserved(t.Key, p.columns); err != nil {
		return nil, nil, err
	}
	top := meta.TopSegment(t.Key)
	fields := meta.Segments(t.Key)[1:]
	var hits []*keyDef
	for i := range p.reg {
		d := &p.reg[i]
		if d.name != top || d.retired {
			continue
		}
		if t.Level != "" && !p.inPlace(d.src, t) {
			continue
		}
		hits = append(hits, d)
	}
	switch len(hits) {
	case 0:
		if t.Level != "" && p.home(t) == "" {
			return nil, nil, errs.New(errs.MetaUnregistered, "meta: no "+strings.TrimSpace(t.Level+" "+t.Place)+" place is attached here, so "+top+" cannot be registered there").
				WithHint("drop the scope to register it where the row lives, or attach the store (lore store list)")
		}
		return nil, fields, nil
	case 1:
		return hits[0], fields, nil
	}
	if p.ask != nil {
		// P2-META-8: a terminal asks per definition, nearest place first
		// (the reg order); the first yes is used; all no → the error below.
		for _, h := range hits {
			ok, err := p.ask(fmt.Sprintf("%q has %d definitions; use %s (%s, %s)?", top, len(hits), h.label(), h.id, h.typ))
			if err != nil {
				return nil, nil, err
			}
			if ok {
				return h, fields, nil
			}
		}
	}
	var lines, forms []string
	for _, h := range hits {
		in := ""
		if h.internal {
			in = "  internal"
		}
		lines = append(lines, fmt.Sprintf("  %s  %s  %s%s", h.label(), h.id, h.typ, in))
		forms = append(forms, h.id.String()+"="+t.Value)
	}
	return nil, nil, errs.New(errs.MetaAmbiguous, fmt.Sprintf("%q has %d definitions this row can see:\n%s", top, len(hits), strings.Join(lines, "\n"))).
		WithHint("name the definition: " + strings.Join(forms, "   ") + " (or a scope form such as repo:" + top + "=…)")
}

// metaLevel is the grammar level of an attached store, from its settings
// level: the repo, a project folder, or the org (the org folder or a store
// root). The personal store has no level ("").
func (p *metaPlan) metaLevel(src store.Source) string {
	switch levelOf(p.e.stores[src]) {
	case policy.LevelRepo:
		return meta.LevelRepo
	case policy.LevelFolder:
		return meta.LevelProject
	case policy.LevelOrg, policy.LevelStoreRoot:
		return meta.LevelOrg
	case policy.LevelPersonal:
	}
	return ""
}

// inPlace reports whether src is the place a scoped term names: its level,
// and, with a place name, that store or folder exactly.
func (p *metaPlan) inPlace(src store.Source, t meta.Term) bool {
	if p.metaLevel(src) != t.Level {
		return false
	}
	if t.Place == "" {
		return src == p.nearest(t.Level)
	}
	_, f, _ := strings.Cut(string(src), folderSep)
	return string(src) == t.Place || f == t.Place
}

// nearest is the first attached store of a level ("" when none).
func (p *metaPlan) nearest(level string) store.Source {
	for _, s := range p.e.Stores() {
		if p.metaLevel(s) == level {
			return s
		}
	}
	return ""
}

// home is where a new key of t is registered: the scoped place, else the
// store the row is written to.
func (p *metaPlan) home(t meta.Term) store.Source {
	if t.Level == "" {
		return p.target
	}
	if t.Place != "" {
		for _, s := range p.e.Stores() {
			if p.inPlace(s, t) {
				return s
			}
		}
	}
	return p.nearest(t.Level)
}

// keyID is the id of the entry a term writes: the definition's, a key
// this write already registers, or a fresh pending one.
func (p *metaPlan) keyID(d *keyDef, t meta.Term, name string, fields []string, vt meta.Type) ids.ID[ids.MetaKey] {
	if d != nil {
		return d.id
	}
	home := p.home(t)
	for _, k := range p.pending {
		if k.name == name && k.home == home {
			return k.id
		}
	}
	typ := vt
	if len(fields) > 0 {
		typ = meta.Type{Kind: value.KindJSON} // META-B7-OBJ
	}
	k := pendingKey{name: name, home: home, typ: typ}
	k.id = p.e.newMetaKeyID()
	p.pending = append(p.pending, k)
	return k.id
}

// newMetaKeyID mints a mky_ id (an id error is impossible for a known prefix
// and leaves the zero id, which the create then refuses).
func (e *Engine) newMetaKeyID() ids.ID[ids.MetaKey] {
	raw, err := e.ids.NewID(ids.PrefixMetaKey)
	if err != nil {
		return ids.ID[ids.MetaKey]{}
	}
	id, err := ids.Parse[ids.MetaKey](raw.String())
	if err != nil {
		return ids.ID[ids.MetaKey]{}
	}
	return id
}

// allowed maps a value of a key with allowed values to the value's id and
// name (OQ-AV): by name, then label (case-insensitive), then the converted
// name. A list maps item by item.
func allowed(d keyDef, raw []byte) (v, vname []byte, err error) {
	one := func(item []byte) ([]byte, []byte, error) {
		s, uerr := unquoteJSON(item)
		if uerr != nil {
			return nil, nil, errs.New(errs.MetaValue, "meta: "+d.name+" takes one of its allowed values")
		}
		conv := meta.ToSnake(s)
		for _, a := range d.values {
			if a.name == s || (a.label != "" && strings.EqualFold(a.label, s)) || a.name == conv {
				return canon.String(a.id.String()), canon.String(a.name), nil
			}
		}
		names := make([]string, len(d.values))
		for i, a := range d.values {
			names[i] = a.name
		}
		return nil, nil, errs.New(errs.MetaValue, fmt.Sprintf("meta: %q is not an allowed value of %s; allowed: %s", s, d.name, strings.Join(names, ", "))).
			WithHint("lore meta value add " + d.name + " <value>")
	}
	if canon.KindOf(raw) != canon.KindArray {
		return one(raw)
	}
	items, err := canon.Elements(raw)
	if err != nil {
		return nil, nil, errs.Wrap(errs.Internal, "meta: list", err)
	}
	var vs, ns [][]byte
	for _, it := range items {
		a, b, oerr := one(it)
		if oerr != nil {
			return nil, nil, oerr
		}
		vs, ns = append(vs, a), append(ns, b)
	}
	if v, err = canon.Compact(append(append([]byte("["), joinRaw(vs)...), ']')); err != nil {
		return nil, nil, errs.Wrap(errs.Internal, "meta: list", err)
	}
	if vname, err = canon.Compact(append(append([]byte("["), joinRaw(ns)...), ']')); err != nil {
		return nil, nil, errs.Wrap(errs.Internal, "meta: list", err)
	}
	return v, vname, nil
}

// joinRaw joins JSON items with commas.
func joinRaw(items [][]byte) []byte {
	var out []byte
	for i, it := range items {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, it...)
	}
	return out
}

// unquoteJSON reads a JSON string.
func unquoteJSON(raw []byte) (string, error) {
	if canon.KindOf(raw) != canon.KindString {
		return "", errs.New(errs.MetaValue, "not a string")
	}
	v, err := value.Parse(value.KindText, raw)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// unset removes a key (or a field path of it) from the row's entries.
func (p *metaPlan) unset(rowID ids.AnyID, name string) error {
	t, err := meta.Parse(name + "=x")
	if err != nil {
		return err
	}
	var top string
	var path []string
	if !t.KeyID.IsZero() {
		path = t.Fields
	} else {
		top, path = meta.TopSegment(t.Key), meta.Segments(t.Key)[1:]
	}
	found := false
	for _, id := range sortedIDs(p.entries) {
		en := p.entries[id]
		if (t.KeyID.IsZero() && en.Name != top) || (!t.KeyID.IsZero() && id != t.KeyID) {
			continue
		}
		if len(path) == 0 {
			delete(p.entries, id)
			found = true
			continue
		}
		out, empty, ok, uerr := meta.UnsetPath(en.V, path)
		if uerr != nil {
			return uerr
		}
		if !ok {
			continue
		}
		found = true
		if empty {
			delete(p.entries, id)
			continue
		}
		en.V = out
		p.entries[id] = en
	}
	if !found {
		have := make([]string, 0, len(p.entries))
		for _, en := range p.entries {
			have = append(have, en.Name)
		}
		slices.Sort(have)
		list := "it has no metadata"
		if len(have) > 0 {
			list = "it has: " + strings.Join(have, ", ")
		}
		return errs.New(errs.MetaUnknown, fmt.Sprintf("%s has no metadata key %q; %s", rowID, name, list))
	}
	return nil
}

// maxDepth is the team's meta.max_depth for the target (3.13, OV56); 0
// (unset) lets meta.CheckName use meta.MaxDepth.
func (p *metaPlan) maxDepth() (int, error) {
	pol, _, err := p.e.settings.Policy(p.target)
	if err != nil {
		return 0, err
	}
	n, ok := pol.Limit(policy.KeyMetaMaxDepth)
	if ok && n < 1 {
		n = 1 // a set 0 is the strictest place, never "use the default" (stricter wins)
	}
	return int(n), nil
}

// checkSize applies meta.max_bytes and meta.warn_bytes (3.13).
func (p *metaPlan) checkSize() error {
	pol, _, err := p.e.settings.Policy(p.target)
	if err != nil {
		return err
	}
	limit, _ := pol.Limit(policy.KeyMetaMaxBytes)
	warnAt, _ := pol.Limit(policy.KeyMetaWarnBytes) // OV56: unset → meta.WarnBytes
	entries := make([]meta.Entry, 0, len(p.entries))
	for _, id := range sortedIDs(p.entries) {
		entries = append(entries, p.entries[id])
	}
	w, err := meta.CheckSizeWarn(entries, limit, warnAt)
	if err != nil {
		return err
	}
	if w != "" {
		p.warn("meta", w)
	}
	return nil
}

// scanMeta runs the secret scan over the changed values. A finding is
// refused whatever secrets.on_detect says (DECISION META-B7-SCAN: ask and
// redact for metadata come with the leak guards of B9).
func (e *Engine) scanMeta(cl call, target store.Source, rowID ids.AnyID, p *metaPlan) error {
	_, rules, err := e.settings.Policy(target)
	if err != nil {
		return err
	}
	allows, err := e.settings.Allows(target)
	if err != nil {
		return err
	}
	own := e.actorOf(cl) == e.actor
	for _, id := range p.changed {
		en, ok := p.entries[id]
		if !ok {
			continue
		}
		field := row.KeyMeta + "." + en.Name
		found, _ := e.scanText(field, string(en.V))
		var kept []scan.Finding
		for _, f := range found {
			if !rules.IsOff(f.RuleID, own) {
				kept = append(kept, f)
			}
		}
		if len(scan.Filter(rowID, kept, allows)) > 0 {
			return errs.New(errs.SecretDetected, "meta: the value of "+en.Name+" looks like a secret").
				WithHint("store it in your vault and write ${secret:…} instead")
		}
	}
	return nil
}

// nearShown is how many near-duplicate names a new key's warning lists
// (METADATA 7: "the three nearest existing keys").
const nearShown = 3

// visibleNames are the names of the live (not retired) definitions this
// write can see.
func (p *metaPlan) visibleNames() []string {
	var out []string
	for _, d := range p.reg {
		if !d.retired && !slices.Contains(out, d.name) {
			out = append(out, d.name)
		}
	}
	return out
}

// register writes the key rows this write created (auto-registration,
// 3.9 (a)): internal when first registered in a store (R2-Q2); added_by
// and reviewed record an agent's key for review (MQ7).
func (p *metaPlan) register(ctx context.Context, cl call) error {
	byWhom, reviewed := schema.MetaAddedByPerson, true
	if cl.caller.Agent != "" {
		byWhom, reviewed = schema.MetaAddedByAgent, false
	}
	for _, k := range p.pending {
		scope := metaScope(k.home)
		fields := map[string]value.Value{
			mkStrict: value.BoolOf(false), mkInternal: value.BoolOf(k.home != store.SourceRepo), mkReviewed: value.BoolOf(reviewed),
		}
		for f, s := range map[string]string{mkName: k.name, mkScope: scope, mkType: k.typ.String(), mkAddedBy: byWhom} {
			t, err := value.TextOf(s)
			if err != nil {
				return err
			}
			fields[f] = t
		}
		if k.typ.Kind == value.KindList {
			// meta_keys.type holds a value kind, so a list key is "list"
			// (DECISION META-B7-LIST: list<elem> needs an elem column)
			t, err := value.TextOf(string(value.KindList))
			if err != nil {
				return err
			}
			fields[mkType] = t
		}
		if _, err := p.e.CreateWithID(ctx, k.home, schema.TableMetaKeys, k.id.Any(), fields); err != nil {
			return err
		}
		what := k.name + " (" + k.typ.String() + ")"
		p.warn(k.name, "new keys in "+scope+": "+what)
		if near := meta.Nearest(k.name, p.visibleNames(), nearShown); len(near) > 0 {
			// 3.5 / P2-META-7: a new key close to an existing one warns
			p.warn(k.name, "did you mean "+strings.Join(near, ", ")+"? "+k.name+" is a new key; fold it with lore meta edit "+k.name+" --merge-into "+near[0])
		}
		if k.home != store.SourceRepo {
			p.warn(k.name, "internal: keys first registered in the store start internal; to let one reach client repos: lore meta edit "+k.name+" --no-internal")
		}
		if !reviewed {
			p.warn(k.name, "unreviewed: a person approves it with lore meta describe "+k.name+" \"…\" (lore meta keys --unreviewed)")
		}
	}
	return nil
}

// sortedIDs are the entry ids in byte order (deterministic messages).
func sortedIDs(m map[ids.ID[ids.MetaKey]]meta.Entry) []ids.ID[ids.MetaKey] {
	out := slices.Collect(maps.Keys(m))
	slices.SortFunc(out, func(a, b ids.ID[ids.MetaKey]) int { return strings.Compare(a.String(), b.String()) })
	return out
}

// MetaFlags are the one shared metadata flag set (3.2, R13): an add or edit
// input embeds it (no json tag) and Run binds it to the command's write,
// so every entity reads --meta, --meta-json and --meta-stdin the same way.
type MetaFlags struct {
	Meta      []string `json:"meta,omitempty" flag:"meta" desc:"set metadata: key=value, key:type=value, project:key=value (repeatable)"`
	MetaJSON  []string `json:"meta_json,omitempty" flag:"meta-json" desc:"set a JSON value: key=<json>, key=@file.json or key=- for stdin (repeatable)"`
	MetaStdin string   `json:"meta_stdin,omitempty" flag:"meta-stdin" desc:"read the value of this key from stdin (keeps it out of shell history)"`
}

// Input field names of MetaFlags (Run reads them from any input).
const (
	inputMeta      = "meta"
	inputMetaJSON  = "meta_json"
	inputMetaStdin = "meta_stdin"
)

// metaFromInput collects the MetaFlags of a decoded input (nil-safe: an
// input without them gives an empty change).
func metaFromInput(in op.Input) MetaFlags {
	var f MetaFlags
	for _, tv := range in.Texts() {
		switch tv.Field {
		case inputMeta:
			f.Meta = append(f.Meta, tv.Text)
		case inputMetaJSON:
			f.MetaJSON = append(f.MetaJSON, tv.Text)
		case inputMetaStdin:
			f.MetaStdin = tv.Text
		}
	}
	return f
}

// Empty reports that no metadata flag was given.
func (f MetaFlags) Empty() bool { return len(f.Meta) == 0 && len(f.MetaJSON) == 0 && f.MetaStdin == "" }

// maxMetaInput caps a value read from a file or stdin (the import cap of
// 3.13: 64 KB of values per row).
const maxMetaInput = 64 << 10

// metaStdinMark is the --meta-json value that reads stdin.
const metaStdinMark = "-"

// Write parses the flags into a MetaWrite bound to table/id (both zero:
// the command's first metadata write); unset names keys to remove.
func (f MetaFlags) Write(table row.Table, id ids.AnyID, unset []string) (MetaWrite, error) {
	mw := MetaWrite{Table: table, ID: id, Unset: unset}
	for _, a := range f.Meta {
		t, err := meta.ParseWrite(a)
		if err != nil {
			return MetaWrite{}, err
		}
		mw.Set = append(mw.Set, t)
	}
	stdinUsed := false
	readStdin := func() (string, error) {
		if stdinUsed {
			return "", errs.New(errs.InvalidInput, "meta: only one value may come from stdin")
		}
		stdinUsed = true
		return readMetaInput(os.Stdin, "stdin")
	}
	for _, a := range f.MetaJSON {
		t, err := meta.ParseWrite(a)
		if err != nil {
			return MetaWrite{}, err
		}
		switch {
		case t.Value == metaStdinMark:
			t.Value, err = readStdin()
		case strings.HasPrefix(t.Value, "@"):
			t.Value, err = readMetaFile(t.Value[1:])
		}
		if err != nil {
			return MetaWrite{}, err
		}
		mw.JSON = append(mw.JSON, t)
	}
	if f.MetaStdin != "" {
		t, err := meta.ParseWrite(f.MetaStdin + "=x")
		if err != nil {
			return MetaWrite{}, err
		}
		if t.Value, err = readStdin(); err != nil {
			return MetaWrite{}, err
		}
		t.Value = strings.TrimSuffix(strings.TrimSuffix(t.Value, "\n"), "\r")
		mw.Set = append(mw.Set, t)
	}
	return mw, nil
}

// readMetaFile reads a --meta-json @file value.
func readMetaFile(path string) (string, error) {
	fh, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", errs.Wrap(errs.InvalidInput, "meta: read "+path, err)
	}
	defer func() { _ = fh.Close() }()
	return readMetaInput(fh, path)
}

// readMetaInput reads at most maxMetaInput bytes.
func readMetaInput(r io.Reader, what string) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxMetaInput+1))
	if err != nil {
		return "", errs.Wrap(errs.InvalidInput, "meta: read "+what, err)
	}
	if len(b) > maxMetaInput {
		return "", errs.New(errs.MetaSize, "meta: the value from "+what+" is larger than 64 KB")
	}
	return string(b), nil
}

// bindInputMeta is Run's step: an input's --meta flags ride on the
// command's first metadata write. check, run after the operation, refuses
// flags no write took (E_META_TABLE), so a value is never dropped silently.
func bindInputMeta(ctx context.Context, in op.Input) (context.Context, func() error, error) {
	none := func() error { return nil }
	f := metaFromInput(in)
	if f.Empty() {
		return ctx, none, nil
	}
	mw, err := f.Write("", ids.AnyID{}, nil)
	if err != nil {
		return nil, none, err
	}
	mw.prefer = opTable(in.Op())
	ctx = WithMeta(ctx, mw)
	bound, _ := ctx.Value(metaKey{}).(MetaWrite)
	return ctx, func() error {
		if bound.used != nil && !bound.used.Load() {
			return errs.New(errs.MetaTable, "meta: "+in.Op()+" wrote no row that takes metadata; the --meta values were not stored").
				WithHint("lore meta set <id> key=value")
		}
		return nil
	}, nil
}

// opTable is the metadata table an op's noun names ("task.add" → tasks,
// "architecture_note.edit" → architecture_notes), or "" when the noun is
// no such table (then the first metadata write takes the flags).
func opTable(opName string) row.Table {
	noun, _, _ := strings.Cut(opName, ".")
	n := strings.ReplaceAll(noun, "_", "")
	if n == "" {
		return ""
	}
	plurals := []string{n + "s", n + "es", strings.TrimSuffix(n, "y") + "ies"}
	for t := range metaTables {
		if slices.Contains(plurals, strings.ReplaceAll(string(t), "_", "")) {
			return t
		}
	}
	return ""
}

// AgentOf is the agent name of the running call ("" for a person or a
// write outside Run): the settings ops record added_by/reviewed with it (MQ7).
func AgentOf(ctx context.Context) string {
	if c, ok := ctx.Value(callKey{}).(call); ok {
		return c.caller.Agent
	}
	return ""
}
