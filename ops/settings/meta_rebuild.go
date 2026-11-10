package settings

// `lore meta rebuild [--in <place>] [--dry-run] [--allow-shallow]`
// (METADATA 3.18, R6; OV64): recreate key rows of ONE place that are
// really lost (missing in the work tree, HEAD, the default branch and
// every fetched ref, and no tombstone says they were forgotten or moved).
//
//	metaRebuild ─► ids data rows carry (live + trash) that no live key row
//	  and no tombstone in any attached store knows
//	  ─► split by the entry's scope label: this place's (rebuilt here) or
//	     another's (named, never written here; scope comes from the place)
//	  ─► KeyRowProber (wire): not git → E_UNSUPPORTED; shallow or sparse
//	     without --allow-shallow → E_REFUSED; a readable copy on any ref
//	     → E_REFUSED naming it (lore never runs git checkout)
//	  ─► per id: (a) git history, last committed version, exact; else
//	     (b) inferred from data rows (types never date/datetime; rebuilt,
//	     reviewed:false, internal in a store, sensitive when the rows say;
//	     allowed values with their own val_ ids and v_name names)
//	     (a copy that exists but does not decode is damaged → E_REFUSED,
//	     OV64-SPLIT; per-ref absent counts reported)
//	  ─► --dry-run: report, stop ─► ask ─► hold the store's sync lock
//	     (StoreHolder, OV64-HOLD) ─► probe again (re-check right before
//	     writing) ─► CreateWithID per key, then its values ─► release

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// OpMetaRebuild is `lore meta rebuild`.
const OpMetaRebuild = "meta.rebuild"

// mRebuilt marks a key row recreated from data rows (3.8; schema field
// "rebuilt", added with OV64).
const mRebuilt = "rebuilt"

// Sources of a rebuilt key row.
const (
	rebuildFromHistory = "history"
	rebuildFromRows    = "rows"
)

// KeyRowProbe is what wire found about a store's git checkout for rebuild.
type KeyRowProbe struct {
	Git     bool // false: a plain folder store (no history, no refs)
	Shallow bool // a shallow clone: history is incomplete
	Sparse  bool // a sparse checkout leaves meta_keys/ or meta_values/ out
	// Found maps a row id to the first place a readable copy exists
	// ("the work tree", "HEAD", a branch or remote-tracking ref).
	Found map[string]string
	// Damaged maps a row id with no readable copy to the first place a
	// copy exists that does not decode (OV64-SPLIT: damaged, not lost).
	Damaged map[string]string
	// AbsentOn counts, per ref searched, the wanted rows missing there.
	AbsentOn map[string]int
	Refs     []string // the refs searched
}

// StoreHolder is the optional Env upgrade that holds a store's backend
// lock (the sync lock) for ctx (wire.App; OV64-HOLD).
type StoreHolder interface {
	HoldStore(ctx context.Context, src store.Source) (held context.Context, release func() error, err error)
}

// KeyRowProber is the optional Env upgrade rebuild needs (wire.App).
type KeyRowProber interface {
	ProbeRows(ctx context.Context, src store.Source, refs []engine.Ref) (KeyRowProbe, error)
}

// idCreator writes a row under a pinned id (wire.App, engine.CreateWithID):
// a rebuilt row keeps the id every data row already carries.
type idCreator interface {
	CreateWithID(ctx context.Context, target store.Source, table row.Table, id ids.AnyID, fields map[string]value.Value) (engine.Written, error)
}

// MetaRebuildIn is the input of meta.rebuild.
type MetaRebuildIn struct {
	In           string `json:"in,omitempty" flag:"in" desc:"the place whose key rows are rebuilt (repo, <store>, <store>/<folder>; default repo)"`
	DryRun       bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"list what would be rebuilt; write nothing"`
	AllowShallow bool   `json:"allow_shallow,omitempty" flag:"allow-shallow" desc:"rebuild on a shallow clone or a sparse checkout that leaves the metadata folders out"`
}

// MetaRebuildKey is one key rebuild would recreate (or did).
type MetaRebuildKey struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Source string   `json:"source"` // history | rows
	Commit string   `json:"commit,omitempty"`
	Values []string `json:"values,omitempty"` // allowed value names rebuilt with it
	Note   string   `json:"note,omitempty"`
	// Internal / Sensitive: the flags the rebuilt row starts with.
	Internal  bool `json:"internal,omitempty"`
	Sensitive bool `json:"sensitive,omitempty"`
}

// MetaRebuildElsewhere is a missing id labelled with another place.
type MetaRebuildElsewhere struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

// MetaRebuildOut is the output of meta.rebuild.
type MetaRebuildOut struct {
	Store     string                 `json:"store"`
	Keys      []MetaRebuildKey       `json:"keys"`
	Elsewhere []MetaRebuildElsewhere `json:"elsewhere"`
	DryRun    bool                   `json:"dry_run,omitempty"`
	Written   int                    `json:"written"`
	// Refs are the refs searched, each with how many of the missing rows
	// it lacks (OV64-SPLIT, the per-ref counts).
	Refs []MetaRebuildRef `json:"refs,omitempty"`
}

// MetaRebuildRef is one searched ref and its count of absent rows.
type MetaRebuildRef struct {
	Ref    string `json:"ref"`
	Absent int    `json:"absent"`
}

// Text implements op.Texter (the 3.18 sample).
func (o MetaRebuildOut) Text() string {
	var b strings.Builder
	if len(o.Keys) == 0 && len(o.Elsewhere) == 0 {
		return "✓ no key row of " + place(store.Source(o.Store)) + " is missing; nothing to rebuild\n"
	}
	var hist, rows []MetaRebuildKey
	for _, k := range o.Keys {
		if k.Source == rebuildFromHistory {
			hist = append(hist, k)
		} else {
			rows = append(rows, k)
		}
	}
	fmt.Fprintf(&b, "key rows of %s: %d missing in the work tree, HEAD, every branch and fetched ref\n", place(store.Source(o.Store)), len(o.Keys))
	if len(o.Refs) > 0 {
		counts := make([]string, 0, len(o.Refs))
		for _, ref := range o.Refs {
			counts = append(counts, fmt.Sprintf("%s %d", ref.Ref, ref.Absent))
		}
		b.WriteString("searched (rows absent per ref): " + strings.Join(counts, ", ") + "\n")
	}
	if len(hist) > 0 {
		names := make([]string, 0, len(hist))
		for _, k := range hist {
			names = append(names, k.Name)
		}
		fmt.Fprintf(&b, "from git history, exact: %d keys\n  %s\n", len(hist), strings.Join(names, ", "))
	}
	if len(rows) > 0 {
		b.WriteString("from row files (ids labelled " + scopeOf(store.Source(o.Store)) + ", not in history):\n")
		for _, k := range rows {
			line := fmt.Sprintf("  %s  %s  %s", short(anyID(k.ID)), k.Name, k.Type)
			if len(k.Values) > 0 {
				line += "  values: " + strings.Join(k.Values, ", ")
			}
			if k.Note != "" {
				line += "  " + k.Note
			}
			b.WriteString(line + "\n")
		}
	}
	for _, e := range o.Elsewhere {
		fmt.Fprintf(&b, "not rebuilt here (defined elsewhere): %s %s (%s): rebuild or sync it in that place\n", short(anyID(e.ID)), e.Name, orUnknown(e.Scope))
	}
	if len(rows) > 0 {
		fmt.Fprintf(&b, "needs review: %d key rows marked rebuilt: true, reviewed: false\n", len(rows))
		if o.Store != string(store.SourceRepo) {
			fmt.Fprintf(&b, "fail closed: %d keys start internal: true (this is a store place)\n", len(rows))
		}
	}
	if o.DryRun {
		b.WriteString("dry run: nothing written\n")
	} else if o.Written > 0 {
		fmt.Fprintf(&b, "✓ rebuilt %d key rows\n", o.Written)
	}
	return b.String()
}

// anyID parses an id for display ("" stays zero).
func anyID(s string) ids.AnyID {
	id, err := ids.ParseAny(s)
	if err != nil {
		return ids.AnyID{}
	}
	return id
}

func (o ops) metaRebuildOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpMetaRebuild, Summary: "Recreate lost metadata key rows of one place from git history or data rows", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(writeErrors), errs.Unsupported, errs.Refused), Confirm: op.ConfirmRule{MayAsk: true}, RESTNoun: metaREST,
			Examples: []op.Example{{Title: "preview", CLI: "lore meta rebuild --dry-run", Want: "nothing"}}}, o.metaRebuild),
	}
}

// missingKey is one key id data rows carry that nothing defines.
type missingKey struct {
	id        ids.ID[ids.MetaKey]
	name      string
	scope     string
	sensitive bool
	strict    bool
	vals      [][]byte          // every stored V
	valueIDs  map[string]string // val_ id → name (v_name)
}

// metaRebuild is the op (see the file's flow sketch; R6, 3.18).
func (o ops) metaRebuild(ctx context.Context, in MetaRebuildIn) (MetaRebuildOut, error) {
	src := targetOf(in.In)
	out := MetaRebuildOut{Store: string(src), Keys: []MetaRebuildKey{}, Elsewhere: []MetaRebuildElsewhere{}, DryRun: in.DryRun}
	mine, elsewhere, err := o.missingKeys(ctx, src)
	if err != nil {
		return MetaRebuildOut{}, err
	}
	out.Elsewhere = elsewhere
	if len(mine) == 0 {
		return out, nil
	}
	pr, ok := upgrade.As[KeyRowProber](o.env)
	if !ok {
		return MetaRebuildOut{}, errs.New(errs.Unsupported, "meta rebuild: this surface cannot read the store's git checkout").WithHint("run it from the lore CLI")
	}
	probe, err := o.probeClean(ctx, pr, src, mine, in.AllowShallow)
	if err != nil {
		return MetaRebuildOut{}, err
	}
	for _, ref := range probe.Refs {
		out.Refs = append(out.Refs, MetaRebuildRef{Ref: ref, Absent: probe.AbsentOn[ref]})
	}
	plans := make([]rebuildPlan, 0, len(mine))
	for _, m := range mine {
		p, perr := o.planKey(ctx, src, m)
		if perr != nil {
			return MetaRebuildOut{}, perr
		}
		plans = append(plans, p)
		out.Keys = append(out.Keys, p.report)
	}
	if in.DryRun {
		return out, nil
	}
	if err = bulk.Ask(ctx, fmt.Sprintf("recreate %d metadata key rows in %s (ids kept)?", len(plans), place(src))); err != nil {
		return MetaRebuildOut{}, err
	}
	// Re-check right before writing: a sync or checkout since the
	// preview may have brought a row back, which must never be overwritten
	// (3.18 step 1). CreateWithID also refuses an id that exists (E_STALE).
	// OV64-HOLD: the store's sync lock is held from this re-check through
	// the last write, so no sync can bring a row back in between.
	if h, hok := upgrade.As[StoreHolder](o.env); hok {
		held, release, herr := h.HoldStore(ctx, src)
		if herr != nil {
			return MetaRebuildOut{}, herr
		}
		defer func() { _ = release() }() // idempotent; the writes are done or failed
		ctx = held
	}
	if _, err = o.probeClean(ctx, pr, src, mine, in.AllowShallow); err != nil {
		return MetaRebuildOut{}, err
	}
	cr, ok := upgrade.As[idCreator](o.env)
	if !ok {
		return MetaRebuildOut{}, errs.New(errs.Unsupported, "meta rebuild needs the engine's pinned-id create")
	}
	for _, p := range plans {
		if _, err = cr.CreateWithID(ctx, src, schema.TableMetaKeys, p.id.Any(), p.fields); err != nil {
			return out, err
		}
		for _, v := range p.values {
			if _, err = cr.CreateWithID(ctx, src, schema.TableMetaValues, v.id, v.fields); err != nil {
				return out, err
			}
		}
		out.Written++
	}
	return out, nil
}

// missingKeys is every key id data rows carry that no attached store
// knows (live or tombstone), split into this place's and another's.
func (o ops) missingKeys(ctx context.Context, src store.Source) ([]missingKey, []MetaRebuildElsewhere, error) {
	known := map[string]bool{}
	for _, s := range engine.ReadStoresOf(o.env) {
		rows, err := o.env.List(ctx, s, schema.TableMetaKeys)
		if err != nil {
			return nil, nil, err
		}
		for _, r := range rows {
			known[r.ID.String()] = true // a tombstone counts: forgotten or moved (DQ4)
		}
	}
	rows, err := o.usedRows(ctx)
	if err != nil {
		return nil, nil, err
	}
	want := scopeOf(src)
	byID := map[string]*missingKey{}
	others := map[string]MetaRebuildElsewhere{}
	for _, r := range rows {
		for kid, en := range r.entries {
			if known[kid.String()] {
				continue
			}
			// R6: the scope is taken from the place; a row label naming
			// another place is never written here.
			if en.Scope != want || !rowSeesPlace(r.Src, src) {
				others[kid.String()] = MetaRebuildElsewhere{ID: kid.String(), Name: en.Name, Scope: en.Scope}
				continue
			}
			m := byID[kid.String()]
			if m == nil {
				m = &missingKey{id: kid, name: en.Name, scope: en.Scope, valueIDs: map[string]string{}}
				byID[kid.String()] = m
			}
			m.sensitive = m.sensitive || en.Sensitive
			m.strict = m.strict || en.Strict
			m.vals = append(m.vals, en.V)
			addValueIDs(m.valueIDs, en)
		}
	}
	mine := make([]missingKey, 0, len(byID))
	for _, m := range byID {
		delete(others, m.id.String())
		mine = append(mine, *m)
	}
	slices.SortFunc(mine, func(a, b missingKey) int { return strings.Compare(a.name+a.id.String(), b.name+b.id.String()) })
	el := make([]MetaRebuildElsewhere, 0, len(others))
	for _, e := range others {
		el = append(el, e)
	}
	slices.SortFunc(el, func(a, b MetaRebuildElsewhere) int { return strings.Compare(a.Name+a.ID, b.Name+b.ID) })
	return mine, el, nil
}

// rowSeesPlace reports that a row in rowSrc can use a key of place: the
// row is in the place itself, in the repo (which sees every attached
// store), or in another folder of the same store.
func rowSeesPlace(rowSrc, place store.Source) bool {
	if rowSrc == place || rowSrc == store.SourceRepo {
		return true
	}
	if place == store.SourceRepo {
		return false
	}
	root := func(s store.Source) string { r, _, _ := strings.Cut(string(s), folderSep); return r }
	return root(rowSrc) == root(place)
}

// addValueIDs records the allowed-value ids an entry stores with their
// v_name names (a scalar, or a list item by item).
func addValueIDs(dst map[string]string, en meta.Entry) {
	if len(en.VName) == 0 {
		return
	}
	var v, n []string
	var s1, s2 string
	if json.Unmarshal(en.V, &s1) == nil && json.Unmarshal(en.VName, &s2) == nil {
		v, n = []string{s1}, []string{s2}
	} else if json.Unmarshal(en.V, &v) != nil || json.Unmarshal(en.VName, &n) != nil || len(v) != len(n) {
		return
	}
	for i := range v {
		if id, err := ids.ParseAny(v[i]); err == nil && id.Prefix() == ids.PrefixMetaValue {
			dst[v[i]] = n[i]
		}
	}
}

// probeClean runs the git checks of 3.18 steps 2 and 3 over the key rows.
func (o ops) probeClean(ctx context.Context, pr KeyRowProber, src store.Source, mine []missingKey, allowShallow bool) (KeyRowProbe, error) {
	refs := make([]engine.Ref, 0, len(mine))
	for _, m := range mine {
		refs = append(refs, engine.Ref{Store: src, Table: schema.TableMetaKeys, ID: m.id.Any()})
	}
	p, err := pr.ProbeRows(ctx, src, refs)
	if err != nil {
		return KeyRowProbe{}, err
	}
	if !p.Git {
		// DECISION OV64-FOLDER: a plain folder store has no refs to search
		// and no history, so "really lost" cannot be told; refuse.
		return KeyRowProbe{}, errs.New(errs.Unsupported, place(src)+" is a plain folder store: lore cannot tell a lost key row from one out of reach").
			WithHint("restore the meta_keys files from your backup; nothing written")
	}
	if (p.Shallow || p.Sparse) && !allowShallow {
		what := "a shallow clone (no full history)"
		if p.Sparse {
			what = "a sparse checkout that leaves meta_keys/ or meta_values/ out"
		}
		return KeyRowProbe{}, errs.New(errs.Refused, place(src)+" is "+what+"; the missing rows may only be out of reach").
			WithHint("fetch the full history or widen the checkout, or rerun with --allow-shallow; nothing written")
	}
	var found []string
	for _, m := range mine {
		if where, ok := p.Found[m.id.String()]; ok {
			found = append(found, fmt.Sprintf("%s (%s) is in %s", m.name, short(m.id.Any()), where))
		}
	}
	if len(found) > 0 {
		slices.Sort(found)
		return KeyRowProbe{}, errs.New(errs.Refused, "not lost: "+strings.Join(found, "; ")).
			WithHint("restore it from there (git checkout <ref> -- <path>, or merge it; lore never does this itself) or run lore sync; nothing written")
	}
	// OV64-SPLIT: a copy that exists but does not decode is damaged, not
	// lost; rebuilding would hide the damage, so refuse and name it.
	var damaged []string
	for _, m := range mine {
		if where, ok := p.Damaged[m.id.String()]; ok {
			damaged = append(damaged, fmt.Sprintf("%s (%s) is damaged in %s", m.name, short(m.id.Any()), where))
		}
	}
	if len(damaged) > 0 {
		slices.Sort(damaged)
		return KeyRowProbe{}, errs.New(errs.Refused, "not lost but damaged: "+strings.Join(damaged, "; ")).
			WithHint("repair the row file (lore doctor names it; git show <ref>:<path> shows the copy); nothing written")
	}
	return p, nil
}

// rebuildPlan is one key row (and its values) to write.
type rebuildPlan struct {
	id     ids.ID[ids.MetaKey]
	fields map[string]value.Value
	values []rebuildValue
	report MetaRebuildKey
}

// rebuildValue is one meta_values row to write.
type rebuildValue struct {
	id     ids.AnyID
	fields map[string]value.Value
}

// planKey picks the source of one key: its last committed version, else
// the data rows (3.18 steps 4-6).
func (o ops) planKey(ctx context.Context, src store.Source, m missingKey) (rebuildPlan, error) {
	if last, ok, err := o.lastVersion(ctx, src, schema.TableMetaKeys, m.id.Any()); err != nil {
		return rebuildPlan{}, err
	} else if ok {
		p := rebuildPlan{id: m.id, fields: last.Row.Fields,
			report: MetaRebuildKey{ID: m.id.String(), Name: text(last.Row, mName), Type: text(last.Row, mType), Source: rebuildFromHistory,
				Commit: last.Commit, Internal: flag(last.Row, mInternal), Sensitive: flag(last.Row, mSensitive)}}
		vs, verr := o.planValues(ctx, src, m, true)
		if verr != nil {
			return rebuildPlan{}, verr
		}
		p.values = vs
		for _, v := range vs {
			p.report.Values = append(p.report.Values, valueName(v))
		}
		return p, nil
	}
	typ, note := inferType(m)
	internal := src != store.SourceRepo
	fields := map[string]value.Value{
		mStrict: value.BoolOf(m.strict), mInternal: value.BoolOf(internal), mSensitive: value.BoolOf(m.sensitive),
		mReviewed: value.BoolOf(false), mRebuilt: value.BoolOf(true),
	}
	if err := optTexts(fields, map[string]string{mName: m.name, mScope: scopeOf(src), mType: typ}); err != nil {
		return rebuildPlan{}, err
	}
	p := rebuildPlan{id: m.id, fields: fields,
		report: MetaRebuildKey{ID: m.id.String(), Name: m.name, Type: typ, Source: rebuildFromRows, Note: note, Internal: internal, Sensitive: m.sensitive}}
	vs, err := o.planValues(ctx, src, m, false)
	if err != nil {
		return rebuildPlan{}, err
	}
	p.values = vs
	for _, v := range vs {
		p.report.Values = append(p.report.Values, valueName(v))
	}
	return p, nil
}

// valueName is a planned value's name for the report.
func valueName(v rebuildValue) string {
	if f, ok := v.fields[mName]; ok && f != nil {
		return f.String()
	}
	return v.id.String()
}

// planValues rebuilds the allowed values data rows name, with their own
// val_ ids (never new ones): the last committed version when history has
// it, else key_id + the v_name name. A value already defined is skipped.
func (o ops) planValues(ctx context.Context, src store.Source, m missingKey, tryHistory bool) ([]rebuildValue, error) {
	have := map[string]bool{}
	for _, s := range engine.ReadStoresOf(o.env) {
		rows, err := o.env.List(ctx, s, schema.TableMetaValues)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			have[r.ID.String()] = true
		}
	}
	vids := make([]string, 0, len(m.valueIDs))
	for vid := range m.valueIDs {
		if !have[vid] {
			vids = append(vids, vid)
		}
	}
	slices.Sort(vids)
	out := make([]rebuildValue, 0, len(vids))
	for _, vid := range vids {
		id, err := ids.ParseAny(vid)
		if err != nil {
			continue
		}
		if tryHistory {
			last, ok, herr := o.lastVersion(ctx, src, schema.TableMetaValues, id)
			if herr != nil {
				return nil, herr
			}
			if ok {
				out = append(out, rebuildValue{id: id, fields: last.Row.Fields})
				continue
			}
		}
		fields := map[string]value.Value{}
		if err = optTexts(fields, map[string]string{mvKeyID: m.id.String(), mName: m.valueIDs[vid]}); err != nil {
			return nil, err
		}
		out = append(out, rebuildValue{id: id, fields: fields})
	}
	return out, nil
}

// lastVersion is the last committed version of a row file in src (ok
// false when history has none). An Env without history has none.
func (o ops) lastVersion(ctx context.Context, src store.Source, table row.Table, id ids.AnyID) (task.RowVersion, bool, error) {
	h, ok := upgrade.As[task.RowHistorian](o.env)
	if !ok {
		return task.RowVersion{}, false, nil
	}
	vers, err := h.RowVersions(ctx, engine.Ref{Store: src, Table: table, ID: id})
	if err != nil || len(vers) == 0 {
		return task.RowVersion{}, false, err
	}
	return vers[len(vers)-1], true, nil
}

// inferType is a key's type from its stored values alone (3.18 step 5):
// numbers → number, true/false → bool, arrays → list, objects → json
// (object), anything else → text; date-looking text stays text with a
// note (never date or datetime without history). Mixed kinds → text.
func inferType(m missingKey) (string, string) {
	kinds := map[value.Kind]int{}
	dates := 0
	for _, raw := range m.vals {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		switch raw[0] {
		case '"':
			kinds[value.KindText]++
			var s string
			if json.Unmarshal(raw, &s) == nil && looksDate(s) {
				dates++
			}
		case '[':
			kinds[value.KindList]++
		case '{':
			kinds[value.KindJSON]++
		case 't', 'f':
			kinds[value.KindBool]++
		default:
			kinds[value.KindNumber]++
		}
	}
	n := len(m.vals)
	if len(kinds) == 1 {
		for k := range kinds {
			if k == value.KindText && dates > 0 {
				return string(k), fmt.Sprintf("from %d values; date-like values are not typed as dates without history (fix: lore meta edit %s --type date)", n, m.name)
			}
			return string(k), fmt.Sprintf("from %d values", n)
		}
	}
	if len(kinds) == 0 {
		return string(value.KindText), "no values to infer from"
	}
	return string(value.KindText), fmt.Sprintf("from %d values of mixed types", n)
}

// looksDate reports a text that parses as a date or an RFC 3339 time.
func looksDate(s string) bool {
	if _, err := time.Parse(dateOnly, s); err == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}
