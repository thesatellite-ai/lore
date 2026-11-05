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
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Key-row maintenance across places (METADATA 3.9 moves, 3.18 rehome and
// adopt; OV58). History-based `meta rebuild` waits for backend history.
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
	// DryRun is accepted for the reference spelling (3.1); rehome is a
	// report in 0.1.18 (OV58), so it never writes either way.
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"preview (rehome only reports in this release; nothing is written either way)"`
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
}

// MetaRehomeOut is the output of meta.rehome.
type MetaRehomeOut struct {
	Clashes []string         `json:"clashes"`
	Missing []MetaRehomeItem `json:"missing"`
	Store   string           `json:"store"`
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
	b.WriteString("nothing written; adopt the keys (lore meta adopt --from <place>) or sync the place that defines them\n")
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
		op.Define(op.Spec{Name: OpMetaRehome, Summary: "List the metadata keys a place's rows use that it can no longer see", Permission: op.PermRead, Access: op.AccessRead,
			Errors: readErrors, RESTNoun: metaREST}, o.metaRehome),
		op.Define(op.Spec{Name: OpMetaMove, Summary: "Move a metadata key row outward to another place (id kept, values unchanged)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: moveErrors, Confirm: op.ConfirmRule{MayAsk: true}, RESTNoun: metaREST,
			Examples: []op.Example{{Title: "repo key to the project", CLI: "lore meta move retry_count --to agency/acme-hub --yes", Want: "moved"}}}, o.metaMoveKey),
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
		return MetaMoveOut{}, errs.New(errs.InvalidInput, "--from and --to name the same place")
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
	}
	have, err := liveRows(ctx, o.env, []store.Source{to}, schema.TableMetaKeys)
	if err != nil {
		return MetaMoveOut{}, err
	}
	if c := clashIn(have, k); c != "" {
		return MetaMoveOut{}, errs.New(errs.MetaTaken, fmt.Sprintf("%s already defines %s (%s)", place(to), name, c)).
			WithHint("settle it with lore meta rename or lore meta edit " + name + " --merge-into <key> first; nothing written")
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
		human += "  new to the store, so it starts internal (lore meta edit " + name + " --no-internal clears it)\n"
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
// Report only (DECISION: re-filing by the copy rule rewrites rows and
// needs chain-change detection; until then the person adopts or syncs).
func (o ops) metaRehome(ctx context.Context, in MetaRehomeIn) (MetaRehomeOut, error) {
	src := targetOf(in.In)
	out := MetaRehomeOut{Clashes: []string{}, Missing: []MetaRehomeItem{}, Store: string(src)}
	keys, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableMetaKeys)
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
	return out, nil
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
