package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Kind layering (RESOURCES 3.6, RQ9, R6): a kind row lives at one level
// (repo, store folder, org folder, store root); a nearer level may add
// fields to it and tighten its fields, never loosen or retype them.
//
//	kindByName ──► kindRows (every store; unreachable ones remembered)
//	   ├─ same name, two ids, two places ─► E_KIND_CONFLICT (until rehome)
//	   └─ not found + a store unreadable  ─► E_RESOURCE_STALE
//	fieldsOf ──► raw rows ─► schema.SplitExtensions ─► schema.FoldExtension
//	   (the effective field as seen from this view; base ids kept)
//	field edit --to <nearer place> ──► extendField: one extension row
//	   (kind_fields.extends = base id), checked by schema.CheckExtension

// kOrigin is the kinds column naming the store kind a client repo's copy
// was made from (RES 3.9).
const kOrigin = "origin"

// levelRank is a level's distance (0 = nearest; policy.Levels order).
func levelRank(l policy.Level) int {
	if i := slices.Index(policy.Levels(), l); i >= 0 {
		return i
	}
	return len(policy.Levels())
}

// nearer reports whether target is a strictly nearer level than def.
func nearer(env Env, target, def store.Source) bool {
	return levelRank(storeLevel(env, target)) < levelRank(storeLevel(env, def))
}

// kindRows reads the live kind rows of every attached store, nearest
// first. A store that cannot be read is skipped and named in unreadable
// (freshness, RES 3.6: the caller decides whether that matters).
func (o ops) kindRows(ctx context.Context) (rows []located, unreadable []store.Source, err error) {
	for _, src := range o.env.Stores() {
		got, lerr := liveRows(ctx, o.env, []store.Source{src}, schema.TableKinds)
		if errs.Has(lerr, errs.StoreUnreachable) {
			unreadable = append(unreadable, src)
			continue
		}
		if lerr != nil {
			return nil, nil, lerr
		}
		rows = append(rows, got...)
	}
	return rows, unreadable, nil
}

// lookupKind finds a live kind by name or id with the layering rules:
// an id always wins; a name must resolve to one kind id across the view
// (a kind copied into a client repo with origin = the store kind's id
// counts as the same kind, 3.9).
func (o ops) lookupKind(ctx context.Context, key string) (located, error) {
	rows, unreadable, err := o.kindRows(ctx)
	if err != nil {
		return located{}, err
	}
	var hits []located
	for _, l := range rows {
		if l.Row.ID.String() == key {
			return l, nil
		}
		if text(l.Row, kName) == key {
			hits = append(hits, l)
		}
	}
	if len(hits) == 0 {
		if len(unreadable) > 0 {
			// RES 3.6 freshness: the defining level may be the one we cannot read.
			return located{}, errs.New(errs.ResourceStale, "no resource kind "+quote(key)+" in the readable stores; store kind rows not available from "+joinSources(unreadable)).
				WithHint("run lore sync, or write at repo level with --to repo")
		}
		return located{}, errs.New(errs.KindUnknown, "no resource kind "+quote(key)).WithHint("run `lore resource kinds` to see the kinds")
	}
	first := hits[0]
	for _, h := range hits[1:] {
		switch {
		case h.Src == first.Src:
			return located{}, errs.New(errs.AmbiguousID, "several kinds are named "+quote(key)+" in "+string(h.Src)).WithHint("name it by its id")
		case h.Row.ID == first.Row.ID, text(first.Row, kOrigin) == h.Row.ID.String(), text(h.Row, kOrigin) == first.Row.ID.String():
			continue
		}
		return located{}, errs.New(errs.KindConflict, fmt.Sprintf("kind %s is defined twice in this view: %s in %s and %s in %s",
			key, first.Row.ID, place(first.Src), h.Row.ID, place(h.Src))).
			WithHint("run lore resource rehome to settle it, or name the kind by its id")
	}
	return first, nil
}

// joinSources lists stores for a message.
func joinSources(srcs []store.Source) string {
	out := make([]string, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, string(s))
	}
	return strings.Join(out, ", ")
}

// kindNameClash refuses a new kind whose name another level of this view
// already defines (RES 3.6: a nearer level extends the inherited kind, it
// does not define a second one).
func (o ops) kindNameClash(ctx context.Context, name string, target store.Source) error {
	rows, _, err := o.kindRows(ctx)
	if err != nil {
		return err
	}
	for _, l := range rows {
		if l.Src != target && text(l.Row, kName) == name {
			return errs.New(errs.KindConflict, fmt.Sprintf("kind %s (%s) is already defined in %s; %s may extend it, not define it again",
				name, l.Row.ID, place(l.Src), place(target))).
				WithHint("add fields with `lore resource field add " + name + " <field> --to " + string(target) + "`")
		}
	}
	return nil
}

// rawFieldsOf are the live kind_fields rows of kind k (plain and
// extension rows) in every store, nearest first.
func (o ops) rawFieldsOf(ctx context.Context, k located) ([]located, error) {
	rows, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableKindFields)
	if err != nil {
		return nil, err
	}
	return where(rows, map[string]string{fKindID: k.Row.ID.String()}), nil
}

// effectiveFields are k's plain field rows with every visible extension
// folded in (RES 3.6); an extension whose base is not visible is skipped.
// A conflicting extension refuses the kind (no writes until fixed).
func effectiveFields(raw []located) ([]located, error) {
	var base, exts []located
	for _, l := range raw {
		if schema.ExtendsOf(l.Row) != "" {
			exts = append(exts, l)
		} else {
			base = append(base, l)
		}
	}
	// Farthest extension first, so the nearest one's pattern/min/max wins.
	for i := len(exts) - 1; i >= 0; i-- {
		e := exts[i]
		for j := range base {
			if base[j].Row.ID.String() != schema.ExtendsOf(e.Row) {
				continue
			}
			folded, err := schema.FoldExtension(base[j].Row, e.Row)
			if err != nil {
				return nil, err
			}
			if folded, err = foldValueIDs(folded, e.Row); err != nil { // OQ-AV
				return nil, err
			}
			base[j].Row = folded
		}
	}
	return base, nil
}

// extensionsOf are the extension rows of base field id.
func extensionsOf(raw []located, id string) []located {
	var out []located
	for _, l := range raw {
		if schema.ExtendsOf(l.Row) == id {
			out = append(out, l)
		}
	}
	return out
}

// checkOuterEdit refuses a change of a defining field row that would put
// an existing nearer extension in conflict (RES 3.6: refused before
// writing).
func (o ops) checkOuterEdit(ctx context.Context, k, f located, patch map[string]value.Value) error {
	raw, err := o.rawFieldsOf(ctx, k)
	if err != nil {
		return err
	}
	exts := extensionsOf(raw, f.Row.ID.String())
	if len(exts) == 0 {
		return nil
	}
	_, base, err := o.env.Get(ctx, engine.Ref{Store: f.Src, Table: schema.TableKindFields, ID: f.Row.ID})
	if err != nil {
		return err
	}
	next := base
	next.Fields = make(map[string]value.Value, len(base.Fields)+len(patch))
	for c, v := range base.Fields {
		next.Fields[c] = v
	}
	for c, v := range patch {
		next.Fields[c] = v
	}
	for _, e := range exts {
		if cerr := schema.CheckExtension(next, e.Row); cerr != nil {
			return errs.New(errs.KindConflict, "this change conflicts with the extension "+e.Row.ID.String()+" in "+place(e.Src)+": "+cerr.Error()).
				WithHint("change the extension in " + place(e.Src) + " first")
		}
	}
	return nil
}

// checkFieldPlace refuses a field written above its kind's level.
func (o ops) checkFieldPlace(k located, target store.Source) error {
	if target == k.Src || nearer(o.env, target, k.Src) {
		return nil
	}
	return errs.New(errs.KindConflict, fmt.Sprintf("kind %s is defined in %s; a field of it may live there or at a nearer level, not in %s",
		text(k.Row, kName), place(k.Src), place(target)))
}

// extendField writes (or updates) the extension row of field f at target
// (`field edit --to <nearer place>`, RES 3.6). Only tightenings are taken;
// every loosening flag is E_KIND_CONFLICT before anything is written.
func (o ops) extendField(ctx context.Context, k, f located, target store.Source, in FieldEditIn) (RowOut, error) {
	if !nearer(o.env, target, f.Src) {
		return RowOut{}, errs.New(errs.KindConflict, fmt.Sprintf("%s.%s is defined in %s; --to must name a nearer place, not %s",
			text(k.Row, kName), text(f.Row, kName), place(f.Src), place(target)))
	}
	var bad []string
	for _, x := range []struct {
		on   bool
		what string
	}{{in.NoRequired, "make it optional"}, {in.NoSensitive, "unmark sensitive"}, {in.NoInternal, "unmark internal"},
		{in.NoStrict, "unmark strict"}, {len(in.RemoveValues) > 0, "remove values"}, {len(in.RenameValues) > 0, "rename values"}, {in.Deprecated || in.NoDeprecated, "change deprecated"},
		{in.Default != "", "change the default"}, {in.ReplacedBy != "", "set replaced-by"}, {in.RemindBefore != "", "set remind-before"},
		{in.Label != "", "change the label"}, {in.Description != "", "change the description"}} {
		if x.on {
			bad = append(bad, x.what)
		}
	}
	if len(bad) > 0 {
		return RowOut{}, errs.New(errs.KindConflict, fmt.Sprintf("%s.%s (%s) is defined in %s; %s may not %s",
			text(k.Row, kName), text(f.Row, kName), f.Row.ID, place(f.Src), place(target), strings.Join(bad, ", "))).
			WithHint("a lower level may add values, make a field required, add pattern/min/max, or mark it sensitive, internal or strict")
	}
	raw, err := o.rawFieldsOf(ctx, k)
	if err != nil {
		return RowOut{}, err
	}
	var existing *located
	for _, e := range extensionsOf(raw, f.Row.ID.String()) {
		if e.Src == target {
			existing = &e
			break
		}
	}
	patch := map[string]value.Value{}
	for _, x := range []struct {
		col string
		on  bool
	}{{fRequired, in.Required}, {fSensitive, in.Sensitive}, {fInternal, in.Internal}, {fStrict, in.Strict}} {
		if x.on {
			patch[x.col] = value.BoolOf(true)
		}
	}
	if err = optTexts(patch, map[string]string{fPattern: in.Pattern, fMin: in.Min, fMax: in.Max}); err != nil {
		return RowOut{}, err
	}
	if len(in.AddValues) > 0 {
		var vals []string
		if existing != nil {
			vals = texts(existing.Row, fValues)
		}
		for _, v := range in.AddValues {
			if !slices.Contains(vals, v) && !slices.Contains(texts(f.Row, fValues), v) {
				vals = append(vals, v)
			}
		}
		if patch[fValues], err = textList(vals); err != nil {
			return RowOut{}, err
		}
		// OQ-AV: an extension's own values get their own val_ ids.
		var prev []ValueEntry
		if existing != nil {
			prev = valueEntries(existing.Row)
		}
		es, verr := syncValueIDs(in.Kind+"."+in.Field, prev, vals, nil, nil)
		if verr != nil {
			return RowOut{}, verr
		}
		if patch[fValueIDs], err = valueEntriesValue(es); err != nil {
			return RowOut{}, err
		}
	}
	if len(patch) == 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "resource field edit: nothing to tighten")
	}
	// Check the result against the defining row before writing.
	_, base, err := o.env.Get(ctx, engine.Ref{Store: f.Src, Table: schema.TableKindFields, ID: f.Row.ID})
	if err != nil {
		return RowOut{}, err
	}
	probe := base
	probe.ID = f.Row.ID
	probe.Fields = map[string]value.Value{}
	if existing != nil {
		probe = existing.Row
		probe.Fields = make(map[string]value.Value, len(existing.Row.Fields))
		for c, v := range existing.Row.Fields {
			probe.Fields[c] = v
		}
	}
	for c, v := range patch {
		probe.Fields[c] = v
	}
	if err = schema.CheckExtension(base, probe); err != nil {
		return RowOut{}, err
	}
	what := fmt.Sprintf("%s.%s (%s): tightened for %s rows", text(k.Row, kName), text(f.Row, kName), short(f.Row.ID), place(target))
	if existing != nil {
		return o.patchRow(ctx, *existing, patch, what)
	}
	if patch[fKindID], err = ref(k.Row.ID); err != nil {
		return RowOut{}, err
	}
	if patch[schema.FieldExtends], err = ref(f.Row.ID); err != nil {
		return RowOut{}, err
	}
	// name and type repeat the base's (both required columns; equal values
	// are not a change, schema.CheckExtension).
	if patch[kName], err = txt(text(f.Row, kName)); err != nil {
		return RowOut{}, err
	}
	if patch[fType], err = txt(text(f.Row, fType)); err != nil {
		return RowOut{}, err
	}
	w, err := o.env.Create(ctx, target, schema.TableKindFields, patch)
	if err != nil {
		return RowOut{}, err
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(target), Human: fmt.Sprintf("✓ %s; extension %s; one commit\n", what, short(w.Row.ID))}, nil
}
