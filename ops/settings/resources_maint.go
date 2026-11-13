package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Resource maintenance (OV32): rename and merge (RES 3.4, 3.6), lint
// (CLI_REFERENCE 3.2 "lore resource lint") and field retype (RES 3.14).
//
//	resource rename <name> <new>        name format + taken check → patch name
//	resource merge <a> --into <b>       same kind → fold unset values into b →
//	                                    repoint refs (one batch) → archive a
//	                                    with replaced_by b
//	resource lint [--fix]               every live row: dangling / archived
//	                                    refs, bare-name refs, missing required,
//	                                    deprecated fields, remind_before, same
//	                                    kind+name twice; --fix resolves bare names
//	resource field retype <k> <f> <t>   every value parses as t → rewrite rows →
//	                                    field type + type_history
const (
	OpResRename    = "resource.rename"
	OpResMerge     = "resource.merge"
	OpResLint      = "resource.lint"
	OpFieldRetype  = "resource.field.retype"
	lintDangling   = "dangling_ref"
	lintArchived   = "archived_ref"
	lintBareName   = "bare_name_ref"
	lintRequired   = "missing_required"
	lintDeprecated = "deprecated_field"
	lintRemind     = "remind"
	lintDuplicate  = "duplicate"
	// lintKindRoute: a kind (from an earlier build or a hand edit) named
	// like a fixed resource command, so its REST route collides (G-META-9).
	lintKindRoute = "kind_route_collision"
)

// ResRenameIn is the input of resource.rename.
type ResRenameIn struct {
	Old string `json:"id" arg:"0" desc:"the resource (name or res_ id)"`
	New string `json:"new_name" arg:"1" desc:"the new name (the kind's name format)"`
	// RewriteRows rewrites the ref label of every resource that refs this
	// one now, in one batch (PASS3-A-3); without it each label refreshes
	// on that row's next write (RES 3.4).
	RewriteRows bool `json:"rewrite_rows,omitempty" flag:"rewrite-rows" desc:"also rewrite the ref label in every resource that refs this one now (one batch)"`
}

// ResMergeIn is the input of resource.merge.
type ResMergeIn struct {
	Name   string `json:"id" arg:"0" desc:"the duplicate to fold away (name or res_ id)"`
	Into   string `json:"into" flag:"into" desc:"the resource that survives (same kind)"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"list what would change; write nothing"`
}

// ResLintIn is the input of resource.lint.
type ResLintIn struct {
	Fix bool `json:"fix,omitempty" flag:"fix" desc:"resolve bare-name refs by lookup (ambiguous ones are left and listed)"`
}

// LintFinding is one problem resource lint found.
type LintFinding struct {
	Check  string `json:"check"`
	Row    string `json:"row"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Field  string `json:"field,omitempty"`
	Detail string `json:"detail"`
	Fixed  bool   `json:"fixed,omitempty"`
}

// ResLintOut is the output of resource.lint.
type ResLintOut struct {
	Count    int           `json:"count"`
	Findings []LintFinding `json:"findings"`
	// Notes: last good definition warnings of conflicted kinds (RES 3.13).
	Notes []string `json:"notes,omitempty"`
}

// Text implements op.Texter.
func (o ResLintOut) Text() string {
	var b strings.Builder
	for _, n := range o.Notes {
		b.WriteString(n + "\n")
	}
	if len(o.Findings) == 0 {
		return b.String() + "no problems found\n"
	}
	for _, f := range o.Findings {
		what := f.Kind + " " + f.Name
		if f.Field != "" {
			what += "." + f.Field
		}
		fixed := ""
		if f.Fixed {
			fixed = "  (fixed)"
		}
		fmt.Fprintf(&b, "%-16s %-30s %s%s\n", f.Check, what, f.Detail, fixed)
	}
	fmt.Fprintf(&b, "%d findings\n", len(o.Findings))
	return b.String()
}

// FieldRetypeIn is the input of resource.field.retype.
type FieldRetypeIn struct {
	Kind   string `json:"kind" arg:"0" desc:"the kind (name or knd_ id)"`
	Field  string `json:"field" arg:"1" desc:"the field name"`
	Type   string `json:"type" arg:"2" desc:"the new value type"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"check every value; write nothing"`
	// KeepAsText leaves values that do not fit as they are (quarantined,
	// R10 3.15) instead of refusing the retype.
	KeepAsText bool `json:"keep_as_text,omitempty" flag:"keep-as-text" desc:"keep values that do not fit the new type unchanged; they are quarantined (listed by show, list and lint)"`
}

func (o ops) maintOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpResRename, Summary: "Rename a resource (ids and refs stay)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.ResourceName), Examples: []op.Example{{Title: "rename a resource", CLI: "lore resource rename --id prod_db --new-name primary_db"}}}, o.resRename),
		op.Define(op.Spec{Name: OpResMerge, Summary: "Fold a duplicate resource into another of the same kind: refs move, values fill, the duplicate is archived", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.ResourceInvalid, errs.ResourceKindMismatch), Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "fold a duplicate", CLI: "lore resource merge --id hub_prod_old --into hub_prod --yes", Want: "merged"}}}, o.resMerge),
		op.Define(op.Spec{Name: OpResLint, Summary: "Find dangling and bare-name refs, missing required fields, deprecated fields, due reminders and duplicates", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: writeErrors, Examples: []op.Example{{Title: "resources that do not fit their kind", CLI: "lore resource lint"}}}, o.resLint),
		op.Define(op.Spec{Name: OpFieldRetype, Summary: "Change a field's type: every value is checked first, then converted (the field id stays)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.ResourceInvalid, errs.ResourceType), Confirm: op.ConfirmRule{MayAsk: true}, Examples: []op.Example{{Title: "preview a type change", CLI: "lore resource field retype --kind database --field port --type int --dry-run"}}}, o.fieldRetype),
	}
}

// resRename: a resource name is a label: the row keeps its id, refs hold the
// id, so no meaning changes (RES 3.4). Referrers' stored ref labels refresh
// on their next write, or all at once with --rewrite-rows (PASS3-A-3).
func (o ops) resRename(ctx context.Context, in ResRenameIn) (RowOut, error) {
	r, err := o.resourceByName(ctx, in.Old, "")
	if err != nil {
		return RowOut{}, err
	}
	d, err := o.kindDef(ctx, text(r.Row, rKind))
	if err != nil {
		return RowOut{}, err
	}
	if err = checkResourceName(d, in.New); err != nil {
		return RowOut{}, err
	}
	rows, err := liveRows(ctx, o.env, []store.Source{r.Src}, schema.TableResources)
	if err != nil {
		return RowOut{}, err
	}
	if hit := where(rows, map[string]string{rKind: d.kind.Row.ID.String(), rName: in.New}); len(hit) > 0 {
		return RowOut{}, errs.New(errs.ResourceTaken, d.name()+" "+in.New+" already exists in "+string(r.Src)+" ("+hit[0].Row.ID.String()+")").
			WithHint("pick another name, or fold the two with lore resource merge")
	}
	v, err := txt(in.New)
	if err != nil {
		return RowOut{}, err
	}
	out, err := o.patchRow(ctx, r, map[string]value.Value{rName: v}, "renamed "+d.name()+" "+text(r.Row, rName)+" → "+in.New+"; ids and refs unchanged")
	if err != nil {
		return out, err
	}
	id, label := r.Row.ID.String(), d.name()+labelSep+in.New
	hits, blocked, err := o.refsTo(ctx, id, label)
	if err != nil {
		return out, err
	}
	if in.RewriteRows {
		if out.Batch, err = o.rewriteRefLabels(ctx, hits, id, label); err != nil {
			return out, err
		}
	}
	out.Human += renameLabelLines(len(hits), blocked, in.RewriteRows, out.Batch)
	return out, nil
}

// resMerge folds in.Name into in.Into (RES 3.6): the survivor keeps its own
// values and gains the duplicate's values for fields it lacks; every ref to
// the duplicate moves to the survivor and the duplicate is archived with
// replaced_by (the repoint path, one batch).
func (o ops) resMerge(ctx context.Context, in ResMergeIn) (ResRepointOut, error) {
	if in.Into == "" {
		return ResRepointOut{}, errs.New(errs.InvalidInput, "resource merge: give --into <name>")
	}
	dup, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return ResRepointOut{}, err
	}
	keep, err := o.resourceByName(ctx, in.Into, "")
	if err != nil {
		return ResRepointOut{}, err
	}
	if dup.Row.ID == keep.Row.ID {
		return ResRepointOut{}, errs.New(errs.InvalidInput, "resource merge: a resource cannot be merged into itself")
	}
	if text(dup.Row, rKind) != text(keep.Row, rKind) {
		// RES 3.6: a merge across kinds is its own exact error.
		return ResRepointOut{}, errs.New(errs.ResourceKindMismatch, "resource merge: "+in.Name+" and "+in.Into+" are of different kinds").
			WithHint("use lore resource repoint for refs between kinds")
	}
	d, err := o.kindDef(ctx, text(keep.Row, rKind))
	if err != nil {
		return ResRepointOut{}, err
	}
	dv, err := d.decode(dup)
	if err != nil {
		return ResRepointOut{}, err
	}
	kv, err := d.decode(keep)
	if err != nil {
		return ResRepointOut{}, err
	}
	var filled []string
	for id, raw := range dv.vals {
		if _, has := kv.vals[id]; !has {
			if _, known := d.byID[id]; known {
				kv.vals[id] = raw
				filled = append(filled, d.fieldNameOf(id))
			}
		}
	}
	slices.Sort(filled)
	note := "values filled from " + text(dup.Row, rName) + ": " + noneIfEmpty(filled) + "\n"
	if in.DryRun {
		out, rerr := o.resRepoint(ctx, ResRepointIn{Old: dup.Row.ID.String(), New: keep.Row.ID.String(), DryRun: true})
		out.Human = "would merge " + text(dup.Row, rName) + " into " + text(keep.Row, rName) + "\n" + out.Human + note
		return out, rerr
	}
	if err = bulk.Ask(ctx, "merge "+d.name()+" "+text(dup.Row, rName)+" into "+text(keep.Row, rName)+"? refs move and "+text(dup.Row, rName)+" is archived"); err != nil {
		return ResRepointOut{}, err
	}
	if len(filled) > 0 {
		patch, perr := o.valuesPatch(ctx, d, kv.vals, kv)
		if perr != nil {
			return ResRepointOut{}, perr
		}
		if perr = o.updateResource(ctx, d, keep, patch); perr != nil {
			return ResRepointOut{}, perr
		}
	}
	out, err := o.resRepoint(ctx, ResRepointIn{Old: dup.Row.ID.String(), New: keep.Row.ID.String()})
	if err != nil {
		return ResRepointOut{}, err
	}
	out.Human = "merged " + d.name() + " " + text(dup.Row, rName) + " into " + text(keep.Row, rName) + "\n" + note + out.Human
	return out, nil
}

func noneIfEmpty(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, ", ")
}

// resLint checks every live resource of the view (CLI_REFERENCE 3.2 lint,
// RES 3.4/3.6/3.14 and the 3.1 remind_before/deprecated options).
func (o ops) resLint(ctx context.Context, in ResLintIn) (ResLintOut, error) {
	rows, err := liveRows(ctx, o.env, o.resStores(), schema.TableResources)
	if err != nil {
		return ResLintOut{}, err
	}
	byID := make(map[string]located, len(rows))
	seen := map[string]located{}
	for _, r := range rows {
		byID[r.Row.ID.String()] = r
	}
	now := opclock.Now(o.env)
	out := ResLintOut{Findings: []LintFinding{}}
	defs := map[string]kindDef{}
	for _, r := range rows {
		kid := text(r.Row, rKind)
		d, ok := defs[kid]
		if !ok {
			if d, err = o.kindDefRead(ctx, kid); err != nil {
				continue // a row of a kind this place cannot see
			}
			defs[kid] = d
			out.Notes = append(out.Notes, d.noteLines()...) // RES 3.13
		}
		name := text(r.Row, rName)
		add := func(check, field, detail string) *LintFinding {
			out.Findings = append(out.Findings, LintFinding{Check: check, Row: r.Row.ID.String(), Kind: d.name(), Name: name, Field: field, Detail: detail})
			return &out.Findings[len(out.Findings)-1]
		}
		key := kid + "/" + name
		if other, dup := seen[key]; dup {
			add(lintDuplicate, "", "same kind and name as "+other.Row.ID.String()+" in "+string(other.Src)+"; fold with lore resource merge")
		}
		seen[key] = r
		if text(r.Row, rArchivedAt) != "" {
			continue
		}
		stored, derr := d.decode(r)
		if derr != nil {
			add(lintDangling, "", derr.Error())
			continue
		}
		fixed := false
		// R10 3.15: values that break their field's rule are listed, never dropped.
		for _, q := range quarantineOf(d, stored) {
			add(lintQuarantined, q.Field, q.Rule+"; fix: lore resource edit --id "+name+" --set "+q.Field+"=<value>")
		}
		for _, fname := range d.fieldNames() {
			f := d.byName[fname]
			fid := f.Row.ID.String()
			raw, has := stored.vals[fid]
			if !has {
				if flag(f.Row, fRequired) {
					add(lintRequired, fname, "required field has no value")
				}
				continue
			}
			v := fromJSONRaw(raw)
			s := display(v)
			if flag(f.Row, fDeprecated) {
				add(lintDeprecated, fname, "deprecated field still has a value"+replacedNote(d, f))
			}
			switch value.Kind(text(f.Row, fType)) {
			case value.KindRef:
				target, inView := byID[s]
				switch {
				case inView && text(target.Row, rArchivedAt) != "":
					add(lintArchived, fname, "points at archived "+text(target.Row, rName))
				case inView:
				case !strings.Contains(s, "_") || !looksLikeID(s):
					// A hand-edited file holding a name, not an id (RES 3.4).
					fd := add(lintBareName, fname, "holds the bare name "+quote(s))
					if in.Fix {
						t, lerr := o.resourceByName(ctx, s, text(f.Row, fRefKind))
						if lerr != nil {
							fd.Detail += "; not fixed: " + lerr.Error()
							continue
						}
						stored.vals[fid] = json.RawMessage(quote(t.Row.ID.String()))
						fd.Fixed, fixed = true, true
					}
				default:
					add(lintDangling, fname, "points at "+s+", not in this view (deleted, or in a place this machine does not see)")
				}
			case value.KindDate, value.KindDateTime:
				if w := remindWindow(f); w > 0 {
					if t, ok := parseWhen(s); ok && !t.Before(now) && t.Sub(now) <= w {
						add(lintRemind, fname, s+" is within "+text(f.Row, fRemindBefore))
					}
				}
			default:
			}
		}
		if fixed {
			patch, perr := o.valuesPatch(ctx, d, stored.vals, stored)
			if perr != nil {
				return ResLintOut{}, perr
			}
			if perr = o.updateResource(ctx, d, r, patch); perr != nil {
				return ResLintOut{}, perr
			}
		}
	}
	kinds, _, err := o.kindRows(ctx)
	if err != nil {
		return ResLintOut{}, err
	}
	reserved := reservedKindNames(o.env)
	for _, k := range kinds {
		if n := text(k.Row, kName); slices.Contains(reserved, n) {
			out.Findings = append(out.Findings, LintFinding{Check: lintKindRoute, Row: k.Row.ID.String(), Kind: n,
				Detail: "kind " + quote(n) + " has the name of a resource command, so its REST route " + resourcesRoutePrefix + n + " runs lore resource " + n + "; rename it: lore resource kind rename --id " + n + " <new_name>"})
		}
	}
	out.Count = len(out.Findings)
	return out, nil
}

func replacedNote(d kindDef, f located) string {
	if rb := text(f.Row, fReplacedBy); rb != "" {
		return "; use " + d.fieldNameOf(rb)
	}
	return ""
}

// looksLikeID reports a "<prefix>_<32 hex>" id shape.
func looksLikeID(s string) bool {
	_, hex, ok := strings.Cut(s, "_")
	if !ok || len(hex) != idHexLen {
		return false
	}
	for _, c := range hex {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// idHexLen is the hex part of a lore id (model/ids: 32-hex UUIDv7).
const idHexLen = 32

// fieldRetype converts every value of a field to a new type (RES 3.14, R12
// item 1): all values are checked first and nothing is written when one
// does not fit; the field keeps its fld_ id and the old type goes into
// type_history. ref and secret_ref are neither source nor target (their
// values are ids and entry names, not convertible text).
func (o ops) fieldRetype(ctx context.Context, in FieldRetypeIn) (RowOut, error) {
	nk, err := value.ParseKind(in.Type)
	if err != nil || nk == value.KindNull {
		return RowOut{}, errs.New(errs.InvalidInput, "type "+quote(in.Type)+" is not a value type")
	}
	d, err := o.kindDef(ctx, in.Kind)
	if err != nil {
		return RowOut{}, err
	}
	f, ok := d.byName[in.Field]
	if !ok {
		return RowOut{}, errs.New(errs.NotFound, "kind "+d.name()+" has no field "+quote(in.Field))
	}
	old := text(f.Row, fType)
	for _, k := range []string{old, in.Type} {
		if k == string(value.KindRef) || k == string(value.KindSecretRef) {
			return RowOut{}, errs.New(errs.InvalidInput, "field retype: "+k+" fields cannot be retyped").WithHint("add a new field and move the values")
		}
	}
	if old == in.Type {
		return RowOut{}, errs.New(errs.InvalidInput, d.name()+"."+in.Field+" is already "+in.Type)
	}
	using, err := o.resourcesUsing(ctx, d.kind, f)
	if err != nil {
		return RowOut{}, err
	}
	type conv struct {
		r      located
		stored resValues
	}
	var convs []conv
	var bad []string
	for _, r := range using {
		stored, derr := d.decode(r)
		if derr != nil {
			return RowOut{}, derr
		}
		v, perr := parseAs(nk, display(fromJSONRaw(stored.vals[f.Row.ID.String()])))
		if perr != nil {
			bad = append(bad, r.Row.ID.String()+" "+text(r.Row, rName))
			continue
		}
		b, merr := v.MarshalJSON()
		if merr != nil {
			return RowOut{}, errs.Wrap(errs.Internal, "encode value", merr)
		}
		stored.vals[f.Row.ID.String()] = b
		convs = append(convs, conv{r: r, stored: stored})
	}
	if len(bad) > 0 && !in.KeepAsText {
		// RES 3.14: values that do not fit the new type are E_RESOURCE_TYPE.
		return RowOut{}, errs.New(errs.ResourceType, fmt.Sprintf("%s not fit %s: %s", plural.Of(len(bad), "value does", "values do"), in.Type, strings.Join(bad, ", "))).
			WithHint("fix them, or pass --keep-as-text to keep values that do not fit as text and quarantine them; nothing written")
	}
	// R10 3.15: --keep-as-text leaves the misfits unchanged; quarantineOf
	// marks them on every read once the field has its new type.
	kept := ""
	if len(bad) > 0 {
		kept = fmt.Sprintf("; %d kept as text and quarantined: %s", len(bad), strings.Join(bad, ", "))
	}
	if in.DryRun {
		return RowOut{DryRun: true, Human: fmt.Sprintf("%s %s%s (dry run, nothing written)\n", plural.Of(len(convs), "value fits", "values fit"), in.Type, kept)}, nil
	}
	if err = bulk.Ask(ctx, "retype "+d.name()+"."+in.Field+" "+old+" → "+in.Type+" and convert "+plural.Count(len(convs), "value")+"?"); err != nil {
		return RowOut{}, err
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindResource)
	if err != nil {
		return RowOut{}, err
	}
	for _, c := range convs {
		patch, perr := o.valuesPatch(ctx, d, c.stored.vals, c.stored)
		if perr != nil {
			return RowOut{}, perr
		}
		if perr = o.updateResource(bctx, d, c.r, patch); perr != nil {
			return RowOut{}, perr
		}
	}
	hist, err := typeHistory(f, old, opclock.Now(o.env).UTC().Format(value.DateTimeLayout))
	if err != nil {
		return RowOut{}, err
	}
	tv, err := txt(in.Type)
	if err != nil {
		return RowOut{}, err
	}
	out, err := o.patchRow(bctx, f, map[string]value.Value{fType: tv, fTypeHistory: hist}, fmt.Sprintf("retyped %s.%s %s → %s; %s converted%s (batch %s)", d.name(), in.Field, old, in.Type, plural.Count(len(convs), "value"), kept, bid))
	out.Batch = bid.String()
	return out, err
}

// typeHistoryEntry is one element of kind_fields.type_history.
type typeHistoryEntry struct {
	Type string `json:"type"`
	At   string `json:"at"`
}

// typeHistory appends {old, at} to f's type_history.
func typeHistory(f located, old, at string) (value.Value, error) {
	var hist []typeHistoryEntry
	// any stored shape: a row read back may hold the list as a value other
	// than value.JSON, so read it through its JSON form
	if raw, ok := f.Row.Fields[fTypeHistory]; ok && raw != nil {
		b, merr := raw.MarshalJSON()
		if merr != nil {
			return nil, errs.Wrap(errs.ConfigInvalid, "field "+f.Row.ID.String()+": type_history", merr)
		}
		if string(b) != "null" {
			if err := json.Unmarshal(b, &hist); err != nil {
				return nil, errs.Wrap(errs.ConfigInvalid, "field "+f.Row.ID.String()+": type_history is not a list", err)
			}
		}
	}
	hist = append(hist, typeHistoryEntry{Type: old, At: at})
	b, err := json.Marshal(hist)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "encode type_history", err)
	}
	return value.JSONOf(b)
}

// parseWhen reads a stored date or datetime value.
func parseWhen(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	t, err := time.Parse(dateOnly, s)
	return t, err == nil
}

// resDeleteWithDependents deletes a resource and every resource that
// refers to it, recursively (the impact set of 3.21, all depths), in one
// delete batch after a confirmation that lists every row (RES 3.23).
func (o ops) resDeleteWithDependents(ctx context.Context, in ResDeleteIn) (ResDeleteOut, error) {
	r, err := o.resourceByName(ctx, in.Name, "")
	if err != nil {
		return ResDeleteOut{}, err
	}
	idx, err := o.buildIndex(ctx, nil)
	if err != nil {
		return ResDeleteOut{}, err
	}
	rootID := r.Row.ID.String()
	nodes, _ := idx.walk(rootID, walkOpts{up: true, depth: len(idx.nodes) + 1})
	targets := []located{r}
	labels := []string{idx.label(rootID)}
	for _, n := range nodes {
		if nd, ok := idx.nodes[n.ID]; ok {
			targets = append(targets, nd.l)
			labels = append(labels, idx.label(n.ID))
		}
	}
	out := ResDeleteOut{ID: rootID, Store: string(r.Src), Count: len(targets), DryRun: in.DryRun}
	for _, t := range targets {
		out.IDs = append(out.IDs, t.Row.ID.String())
	}
	if in.DryRun {
		out.Human = "would delete " + plural.Count(len(targets), "resource") + ": " + strings.Join(labels, ", ") + " (dry run, nothing written)\n"
		return out, nil
	}
	if err = bulk.Ask(ctx, "delete "+plural.Count(len(targets), "resource")+": "+strings.Join(labels, ", ")+"?"); err != nil {
		return ResDeleteOut{}, err
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindDelete)
	if err != nil {
		return ResDeleteOut{}, err
	}
	out.Batch = bid.String()
	for _, t := range targets {
		if _, err = o.env.Delete(bctx, engine.Ref{Store: t.Src, Table: schema.TableResources, ID: t.Row.ID}); err != nil {
			return ResDeleteOut{}, err
		}
	}
	// DQ8 (OV46): one delete wording for every noun, through trashline.
	out.Human = trashline.MovedBatch(plural.Count(len(targets), "resource")+" ("+strings.Join(labels, ", ")+")", out.Batch)
	return out, nil
}
