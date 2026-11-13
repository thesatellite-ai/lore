package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Key-row maintenance across places (METADATA 3.9 moves, 3.18 rehome and
// adopt; OV58). `meta rebuild` is meta_rebuild.go (OV64).
const (
	OpMetaAdopt  = "meta.adopt"
	OpMetaRehome = "meta.rehome"
	OpMetaMove   = "meta.move"
)

// MetaAdoptIn is the input of meta.adopt.
type MetaAdoptIn struct {
	From string `json:"from" flag:"from" desc:"the place whose key rows move here (<store>, <store>/<folder>)"`
	To   string `json:"to,omitempty" flag:"to" desc:"the place that takes them (default repo)"`
	// DryRun previews (CLI_REFERENCE 3.1): the clash check runs, nothing moves.
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"list what would move; write nothing"`
}

// MetaMoveIn is the input of meta.move.
type MetaMoveIn struct {
	Key string `json:"id" arg:"0" desc:"the key (name or mky_ id)"`
	To  string `json:"to" flag:"to" desc:"the place the key row moves to (<store>, <store>/<folder>)"`
}

// MetaMoveOut is the output of meta.adopt and meta.move.
type MetaMoveOut struct {
	Keys   []string `json:"keys"`
	Values int      `json:"values"`
	Store  string   `json:"store"`
	DryRun bool     `json:"dry_run,omitempty"`
	Human  string   `json:"-"`
}

// Text implements op.Texter.
func (o MetaMoveOut) Text() string { return o.Human }

// MetaRehomeIn is the input of meta.rehome.
type MetaRehomeIn struct {
	In string `json:"in,omitempty" flag:"in" desc:"the place whose rows are checked (default repo)"`
	// To is where a key that must be created lands (3.18; default the
	// place --in names).
	To string `json:"to,omitempty" flag:"to" desc:"the place that takes a new key row when no visible key of that name and type exists (default: --in)"`
	// DryRun lists the mapping and writes nothing (FA-META-13).
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"list the keys to re-file and the rows to rewrite; write nothing"`
}

// MetaRehomeItem is one key id a row of the place uses that no visible
// key row defines.
type MetaRehomeItem struct {
	Row   string `json:"row"`
	Key   string `json:"key"`
	Name  string `json:"name"`
	Scope string `json:"scope"`
	// Reuse is a visible key of the same name (the copy rule of MQ15
	// would reuse it when it has the same type), "" when none.
	Reuse string `json:"reuse,omitempty"`
	// To is the key id the row's value is re-filed under (a reused key or
	// a new one), set when rehome writes or plans (FA-META-13).
	To string `json:"to,omitempty"`
}

// MetaRehomeOut is the output of meta.rehome.
type MetaRehomeOut struct {
	Clashes []string         `json:"clashes"`
	Missing []MetaRehomeItem `json:"missing"`
	Store   string           `json:"store"`
	// Plan is one line per re-filed key ("name old → new (reused|new in
	// place)"); Rewritten counts the rows written; Batch names the batch.
	Plan      []string `json:"plan,omitempty"`
	Rewritten int      `json:"rewritten"`
	Batch     string   `json:"batch,omitempty"`
	DryRun    bool     `json:"dry_run,omitempty"`
}

// Text implements op.Texter.
func (o MetaRehomeOut) Text() string {
	var b strings.Builder
	if len(o.Clashes) == 0 && len(o.Missing) == 0 {
		fmt.Fprintf(&b, "✓ every metadata key used in %s is visible from here\n", place(store.Source(o.Store)))
		return b.String()
	}
	for _, c := range o.Clashes {
		b.WriteString("key clash: " + c + "\n")
	}
	for _, m := range o.Missing {
		line := fmt.Sprintf("%s: %s (%s) belongs to %s, which this place does not see", m.Row, m.Name, m.Key, orUnknown(m.Scope))
		if m.Reuse != "" {
			line += "; visible key of that name: " + m.Reuse
		}
		b.WriteString(line + "\n")
	}
	for _, l := range o.Plan {
		b.WriteString("  " + l + "\n")
	}
	switch {
	case len(o.Missing) == 0:
		b.WriteString("nothing to re-file\n")
	case o.DryRun:
		fmt.Fprintf(&b, "dry run: %d rows would be rewritten; nothing written\n", len(o.Missing))
	default:
		fmt.Fprintf(&b, "rehomed %d keys, %d rows rewritten in batch %s (see: lore batch show --id %s)\n", len(o.Plan), o.Rewritten, o.Batch, o.Batch)
	}
	return b.String()
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown place"
	}
	return s
}

func (o ops) metaMoveOps() []op.Entry {
	moveErrors := append(slices.Clone(writeErrors), errs.Unsupported)
	return []op.Entry{
		op.Define(op.Spec{Name: OpMetaAdopt, Summary: "Move another place's metadata key rows into this place (ids kept)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: moveErrors, Confirm: op.ConfirmRule{MayAsk: true}, RESTNoun: metaREST,
			Examples: []op.Example{{Title: "a project merge", CLI: "lore meta adopt --from agency/acme-mobile --to agency/acme-hub --yes", Want: "adopted"}}}, o.metaAdopt),
		op.Define(op.Spec{Name: OpMetaRehome, Summary: "Re-file the metadata keys a place's rows use that it can no longer see (copy rule: reuse a visible key of that name and type, else a new key row)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: moveErrors, Confirm: op.ConfirmRule{MayAsk: true}, RESTNoun: metaREST, Examples: []op.Example{{Title: "see which keys a place lost after its chain changed", CLI: "lore meta rehome --dry-run"}}}, o.metaRehome),
		op.Define(op.Spec{Name: OpMetaMove, Summary: "Move a metadata key row outward to another place (id kept, values unchanged)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: moveErrors, Confirm: op.ConfirmRule{MayAsk: true}, RESTNoun: metaREST,
			Examples: []op.Example{{Title: "repo key to the project", CLI: "lore meta move --id retry_count --to agency/acme-hub --yes", Want: "moved"}}}, o.metaMoveKey),
	}
}

// metaAdopt is `lore meta adopt --from <place> [--to]` (3.18). Flow:
//
//	from's live key rows (and their meta_values) ─► a name the target
//	defines under another id refuses (E_META_TAKEN; DECISION: no automatic
//	fold, rename or --merge-into first) ─► --dry-run: report, stop
//	─► ask (E_NEEDS_CONFIRM)
//	─► each key moves (id kept, tombstone left, engine.Move) with its
//	values, then its scope label is set to the target's
func (o ops) metaAdopt(ctx context.Context, in MetaAdoptIn) (MetaMoveOut, error) {
	if in.From == "" {
		return MetaMoveOut{}, errs.New(errs.InvalidInput, "meta adopt needs --from <place>")
	}
	from, to := store.Source(in.From), targetOf(in.To)
	if from == to {
		return MetaMoveOut{}, errs.New(errs.InvalidInput, "--from and --to name the same place").WithHint("--from is the other place (<store> or <store>/<folder>); --to defaults to this repo")
	}
	keys, err := liveRows(ctx, o.env, []store.Source{from}, schema.TableMetaKeys)
	if err != nil {
		return MetaMoveOut{}, err
	}
	if len(keys) == 0 {
		return MetaMoveOut{}, errs.New(errs.NotFound, "no metadata key rows in "+string(from))
	}
	have, err := liveRows(ctx, o.env, []store.Source{to}, schema.TableMetaKeys)
	if err != nil {
		return MetaMoveOut{}, err
	}
	var clash, names []string
	nvals := 0
	for _, k := range keys {
		name := text(k.Row, mName)
		names = append(names, name)
		if c := clashIn(have, k); c != "" {
			clash = append(clash, fmt.Sprintf("%s (%s here, %s in %s)", name, c, short(k.Row.ID), from))
		}
		vs, verr := o.metaValues(ctx, k)
		if verr != nil {
			return MetaMoveOut{}, verr
		}
		nvals += len(vs)
	}
	if len(clash) > 0 {
		return MetaMoveOut{}, errs.New(errs.MetaTaken, "both places define "+strings.Join(clash, ", ")).
			WithHint("rename one (lore meta rename) or fold it (lore meta edit <key> --merge-into <other>) first; nothing written")
	}
	if err = o.refuseReturn(ctx, keys, from, to); err != nil {
		return MetaMoveOut{}, err
	}
	if in.DryRun {
		return MetaMoveOut{Keys: names, Values: nvals, Store: string(to), DryRun: true,
			Human: fmt.Sprintf("would adopt %d metadata keys (%s) and %d allowed values from %s into %s; ids unchanged\ndry run: nothing written\n", len(keys), strings.Join(names, ", "), nvals, from, place(to))}, nil
	}
	if err = bulk.Ask(ctx, fmt.Sprintf("move %d metadata keys (%s) and %d allowed values from %s into %s? ids stay, values unchanged", len(keys), strings.Join(names, ", "), nvals, from, place(to))); err != nil {
		return MetaMoveOut{}, err
	}
	for _, k := range keys {
		if _, err = o.moveKey(ctx, k, to); err != nil {
			return MetaMoveOut{}, err
		}
	}
	return MetaMoveOut{Keys: names, Values: nvals, Store: string(to),
		Human: fmt.Sprintf("✓ adopted %d metadata keys (%s) and %d allowed values from %s into %s; ids unchanged\n", len(keys), strings.Join(names, ", "), nvals, from, place(to))}, nil
}

// refuseReturn refuses an adopt of key rows that left to before: the
// engine never moves a row back to a place it left (engine.Move, its
// tombstone stays there), so it refused the real run part way, after the
// dry run had said "would adopt". Checked up front, the dry run predicts
// the real run and no key moves before another is refused.
func (o ops) refuseReturn(ctx context.Context, keys []located, from, to store.Source) error {
	var back []string
	for _, k := range keys {
		// the tombstone the move left behind is a row of to that reads as
		// deleted; a key never in to is not found there
		if _, r, gerr := o.env.Get(ctx, engine.Ref{Store: to, Table: schema.TableMetaKeys, ID: k.Row.ID}); gerr == nil && r.Deleted {
			back = append(back, text(k.Row, mName))
		}
	}
	if len(back) == 0 {
		return nil
	}
	return errs.New(errs.Unsupported, "meta adopt: "+strings.Join(back, ", ")+" left "+place(to)+" before, and a key row never returns to a place it left").
		WithHint("keep it in " + string(from) + "; nothing written")
}

// clashIn is the short id of a live key row in have that has k's name
// under another id ("" when none).
func clashIn(have []located, k located) string {
	for _, h := range where(have, map[string]string{mName: text(k.Row, mName)}) {
		if h.Row.ID != k.Row.ID {
			return short(h.Row.ID)
		}
	}
	return ""
}

// metaMoveKey is `lore meta move <key> --to <place>` (3.9 "Moving a
// definition outward", S17). Flow:
//
//	resolve the key ─► --to differs; never into the repo from a store
//	(DECISION: a definition only moves outward, here: never inward to a
//	repo) ─► the target has that name under another id: E_META_TAKEN
//	─► ask ─► moveKey (id kept; the old place keeps a tombstone whose
//	commit carries Lore-Moved-To, and the row's _o records moved_from/to)
func (o ops) metaMoveKey(ctx context.Context, in MetaMoveIn) (MetaMoveOut, error) {
	if in.To == "" {
		return MetaMoveOut{}, errs.New(errs.InvalidInput, "meta move needs --to <store>[/<folder>]")
	}
	k, err := o.metaKey(ctx, in.Key)
	if err != nil {
		return MetaMoveOut{}, err
	}
	to := targetOf(in.To)
	name := text(k.Row, mName)
	switch to {
	case k.Src:
		return MetaMoveOut{}, errs.New(errs.InvalidInput, name+" is already in "+place(to))
	case store.SourceRepo:
		return MetaMoveOut{}, errs.New(errs.InvalidInput, "meta move moves a key outward; "+name+" is in "+string(k.Src)+" and the repo would hide it from every other place").
			WithHint("keep it where it is, or add a repo key under another name")
	case store.SourceMachine:
		// a key is a shared definition; the machine store holds only this
		// machine's machine-local rows, so no key can live there (P3-META-9)
		return MetaMoveOut{}, errs.New(errs.InvalidInput, "meta move cannot move "+name+" into the machine store: it holds only this machine's machine-local rows, never meta keys").
			WithHint("pick a project folder, the org folder, the store root or personal")
	}
	have, err := liveRows(ctx, o.env, []store.Source{to}, schema.TableMetaKeys)
	if err != nil {
		return MetaMoveOut{}, err
	}
	if c := clashIn(have, k); c != "" {
		return MetaMoveOut{}, errs.New(errs.MetaTaken, fmt.Sprintf("%s already defines %s (%s)", place(to), name, c)).
			WithHint("settle it with lore meta rename or lore meta edit --id " + name + " --merge-into <key> first; nothing written")
	}
	// OV58-2 / FB-META-5 (after the name clash, which is settled first): a move goes strictly outward in the level order
	// (repo → folder → org → store root → personal, policy.Levels); a
	// sideways or inward move would hide the key from places that use it.
	if fromL, toL := storeLevel(o.env, k.Src), storeLevel(o.env, to); levelRank(toL) <= levelRank(fromL) {
		return MetaMoveOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("meta move moves a key outward; %s is at level %s in %s and %s is at level %s", name, fromL, place(k.Src), place(to), toL)).
			WithHint("pick a place farther out (a project folder, the org folder, the store root), or use lore meta adopt --from <place> --to <place> to move keys between places")
	}
	vs, err := o.metaValues(ctx, k)
	if err != nil {
		return MetaMoveOut{}, err
	}
	if err = bulk.Ask(ctx, fmt.Sprintf("move %s (%s) from %s to %s? id kept, values unchanged", name, short(k.Row.ID), place(k.Src), place(to))); err != nil {
		return MetaMoveOut{}, err
	}
	internal, err := o.moveKey(ctx, k, to)
	if err != nil {
		return MetaMoveOut{}, err
	}
	human := fmt.Sprintf("moved %s (%s) %s → %s; id kept, values unchanged\n  %s: key row added (%d allowed values)\n  %s: tombstone left\n",
		name, short(k.Row.ID), text(k.Row, mScope), scopeOf(to), place(to), len(vs), place(k.Src))
	if internal {
		human += "  new to the store, so it starts internal (lore meta edit --id " + name + " --no-internal clears it)\n"
	}
	return MetaMoveOut{Keys: []string{name}, Values: len(vs), Store: string(to), Human: human}, nil
}

// moveKey moves key row k and its allowed values to to, keeping ids
// (MQ15 one home per id), then sets the moved row's scope label to the
// target's. A key that leaves the repo for a store starts internal there
// (3.9, 3.11: new to the store); internal reports that. Values move after
// the key so their key_id ref resolves in the target.
func (o ops) moveKey(ctx context.Context, k located, to store.Source) (internal bool, err error) {
	mv, ok := upgrade.As[Mover](o.env)
	if !ok {
		return false, errs.New(errs.Unsupported, "meta move needs the engine's move")
	}
	vs, err := o.metaValues(ctx, k)
	if err != nil {
		return false, err
	}
	if _, err = mv.Move(ctx, engine.Ref{Store: k.Src, Table: schema.TableMetaKeys, ID: k.Row.ID}, to); err != nil {
		return false, err
	}
	for _, v := range vs {
		if _, err = mv.Move(ctx, engine.Ref{Store: v.Src, Table: schema.TableMetaValues, ID: v.Row.ID}, to); err != nil {
			return false, err
		}
	}
	scope, err := txt(scopeOf(to))
	if err != nil {
		return false, err
	}
	patch := map[string]value.Value{mScope: scope}
	internal = k.Src == store.SourceRepo && to != store.SourceRepo && !flag(k.Row, mInternal)
	if internal {
		patch[mInternal] = value.BoolOf(true)
	}
	if _, err = o.env.Update(ctx, engine.Ref{Store: to, Table: schema.TableMetaKeys, ID: k.Row.ID}, patch); err != nil {
		return false, err
	}
	return internal, nil
}

// metaRehome reports what a place's rows can no longer see after its
// chain changed (3.18): key names defined by more than one visible key
// row, and key ids its rows carry that no visible key row defines, each
// with the row's own scope label and a visible key of the same name.
// The missing ids are the chain change (a scope this place no longer
// sees); rehomeRefile re-files them (FA-META-13, replaces OV58-4).
func (o ops) metaRehome(ctx context.Context, in MetaRehomeIn) (MetaRehomeOut, error) {
	src := targetOf(in.In)
	out := MetaRehomeOut{Clashes: []string{}, Missing: []MetaRehomeItem{}, Store: string(src)}
	keys, err := liveRows(ctx, o.env, engine.ReadStoresOf(o.env), schema.TableMetaKeys)
	if err != nil {
		return MetaRehomeOut{}, err
	}
	visible := map[string]bool{}
	byName := map[string][]located{}
	for _, k := range keys {
		visible[k.Row.ID.String()] = true
		if text(k.Row, mMergedInto) == "" {
			byName[text(k.Row, mName)] = append(byName[text(k.Row, mName)], k)
		}
	}
	for name, ks := range byName {
		if len(ks) < 2 {
			continue
		}
		where := make([]string, 0, len(ks))
		for _, k := range ks {
			where = append(where, short(k.Row.ID)+" in "+place(k.Src))
		}
		slices.Sort(where)
		out.Clashes = append(out.Clashes, name+": "+strings.Join(where, ", "))
	}
	slices.Sort(out.Clashes)
	rows, err := o.metaRows(ctx)
	if err != nil {
		return MetaRehomeOut{}, err
	}
	for _, r := range rows {
		if r.Src != src {
			continue
		}
		for _, kid := range sortedKeys(r) {
			if visible[kid.String()] {
				continue
			}
			en := r.entries[kid]
			item := MetaRehomeItem{Row: r.Row.ID.String(), Key: kid.String(), Name: en.Name, Scope: en.Scope}
			if ks := byName[en.Name]; len(ks) > 0 {
				item.Reuse = short(ks[0].Row.ID) + " in " + place(ks[0].Src)
			}
			out.Missing = append(out.Missing, item)
		}
	}
	if len(out.Missing) == 0 {
		return out, nil
	}
	return o.rehomeRefile(ctx, in, src, rows, keys, out)
}

// rehomeRefile re-files every missing key id of src's rows by the copy
// rule of MQ15 (3.18, FA-META-13):
//
//	per missing id ─ visible live key of the same name and type? reuse it
//	               └ else plan a new key row in --to (internal in a store,
//	                 unreviewed, type inferred from the values like rebuild)
//	--dry-run? report ─ ask (bulk.Ask) ─ create the new keys ─ one batch:
//	every row: unset the old id, set the new id (allowed values by name)
//
// The old key row is out of reach, so it records no `copies`; meta_keys
// has no `origin` field yet, so a re-run reuses the key by name instead.
func (o ops) rehomeRefile(ctx context.Context, in MetaRehomeIn, src store.Source, rows []metaRow, keys []located, out MetaRehomeOut) (MetaRehomeOut, error) {
	to := src
	if in.To != "" {
		to = targetOf(in.To)
	}
	type refile struct {
		name, typ string
		reuse     *located
		newID     ids.ID[ids.MetaKey]
		sensitive bool
	}
	plan := map[string]*refile{}
	var order []string
	for _, m := range out.Missing {
		if _, seen := plan[m.Key]; seen {
			continue
		}
		kid, err := ids.Parse[ids.MetaKey](m.Key)
		if err != nil {
			return MetaRehomeOut{}, errs.Wrap(errs.ConfigInvalid, "meta key id", err)
		}
		mk := missingKey{id: kid, name: m.Name}
		for _, r := range rows {
			if en, ok := r.entries[kid]; ok && r.Src == src {
				mk.vals = append(mk.vals, en.V)
				mk.sensitive = mk.sensitive || en.Sensitive
			}
		}
		typ, _ := inferType(mk)
		rf := &refile{name: m.Name, typ: typ, sensitive: mk.sensitive}
		for i := range keys {
			k := keys[i]
			if text(k.Row, mName) == m.Name && text(k.Row, mMergedInto) == "" && sameMetaType(text(k.Row, mType), typ) {
				rf.reuse = &keys[i]
				break
			}
		}
		plan[m.Key] = rf
		order = append(order, m.Key)
	}
	for _, old := range order {
		rf := plan[old]
		if rf.reuse != nil {
			out.Plan = append(out.Plan, fmt.Sprintf("%s %s → %s in %s (reused)", rf.name, old, rf.reuse.Row.ID, place(rf.reuse.Src)))
			continue
		}
		out.Plan = append(out.Plan, fmt.Sprintf("%s %s → a new %s key in %s", rf.name, old, rf.typ, place(to)))
	}
	if in.DryRun {
		out.DryRun = true
		return out, nil
	}
	if err := bulk.Ask(ctx, fmt.Sprintf("re-file %d keys and rewrite %d values of %s?", len(order), len(out.Missing), place(src))); err != nil {
		return MetaRehomeOut{}, err
	}
	for i, old := range order {
		rf := plan[old]
		if rf.reuse != nil {
			nid, err := ids.Parse[ids.MetaKey](rf.reuse.Row.ID.String())
			if err != nil {
				return MetaRehomeOut{}, errs.Wrap(errs.ConfigInvalid, "meta key id", err)
			}
			rf.newID = nid
			continue
		}
		if err := o.checkRegistryCaps(ctx, to, rf.name); err != nil {
			return MetaRehomeOut{}, err
		}
		fields := map[string]value.Value{mStrict: value.BoolOf(false), mInternal: value.BoolOf(to != store.SourceRepo),
			mSensitive: value.BoolOf(rf.sensitive), mReviewed: value.BoolOf(false)}
		desc := "re-filed by lore meta rehome from " + old
		if err := optTexts(fields, map[string]string{mName: rf.name, mScope: scopeOf(to), mType: rf.typ, mDescription: desc, mAddedBy: addedBy(ctx)}); err != nil {
			return MetaRehomeOut{}, err
		}
		w, err := create(ctx, o.env, to, schema.TableMetaKeys, map[string]string{mName: rf.name, mScope: scopeOf(to)}, fields)
		if err != nil {
			return MetaRehomeOut{}, err
		}
		nid, err := ids.Parse[ids.MetaKey](w.Row.ID.String())
		if err != nil {
			return MetaRehomeOut{}, errs.Wrap(errs.ConfigInvalid, "meta key id", err)
		}
		rf.newID = nid
		out.Plan[i] += " " + nid.String()
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindMeta)
	if err != nil {
		return MetaRehomeOut{}, err
	}
	out.Batch = bid.String()
	for i := range out.Missing {
		out.Missing[i].To = plan[out.Missing[i].Key].newID.String()
	}
	for _, r := range rows {
		if r.Src != src {
			continue
		}
		mw := engine.MetaWrite{ID: r.Row.ID}
		for _, old := range order {
			kid, _ := ids.Parse[ids.MetaKey](old) // parsed above
			en, ok := r.entries[kid]
			if !ok {
				continue
			}
			raw := en.V
			if len(en.VName) > 0 {
				raw = en.VName // allowed values move by name, like a fold
			}
			mw.Unset = append(mw.Unset, old)
			if _, has := r.entries[plan[old].newID]; !has {
				mw.JSON = append(mw.JSON, meta.Term{KeyID: plan[old].newID, Value: string(raw)})
			}
		}
		if len(mw.Unset) == 0 {
			continue
		}
		if _, err := o.env.Update(engine.WithMeta(bctx, mw), engine.Ref{Store: r.Src, ID: r.Row.ID}, map[string]value.Value{}); err != nil {
			return MetaRehomeOut{}, errs.Prefix(fmt.Sprintf("meta rehome: stopped after %d rows (batch %s; see: lore batch show --id %s)", out.Rewritten, bid, bid), err)
		}
		out.Rewritten++
	}
	return out, nil
}

// sameMetaType reports whether a key row's stored type word fits the type
// inferred from values: equal, or the key is text-like (every inferred
// text value fits a text key) or list (a list key's row type is "list").
func sameMetaType(keyType, inferred string) bool {
	return keyType == inferred || (inferred == string(value.KindText) && keyType != string(value.KindNumber) && keyType != string(value.KindBool))
}

// sortedKeys is r's key ids in a stable order.
func sortedKeys(r metaRow) []ids.ID[ids.MetaKey] {
	out := make([]ids.ID[ids.MetaKey], 0, len(r.entries))
	for kid := range r.entries {
		out = append(out, kid)
	}
	slices.SortFunc(out, func(a, b ids.ID[ids.MetaKey]) int { return strings.Compare(a.String(), b.String()) })
	return out
}
