package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// lore resource adopt and lore resource rehome (RESOURCES 3.6, R6).
const (
	OpResAdopt  = "resource.adopt"
	OpResRehome = "resource.rehome"
)

// Mover is the optional Env upgrade adopt needs: the engine's Move (the
// row keeps its id, the old copy is tombstoned; wire.App implements it).
type Mover interface {
	Move(ctx context.Context, ref engine.Ref, to store.Source) (engine.Written, error)
}

// AdoptIn is the input of resource.adopt.
type AdoptIn struct {
	From string `json:"from" flag:"from" desc:"the place whose kind and field rows move here (<store>, <store>/<folder>)"`
	To   string `json:"to,omitempty" flag:"to" desc:"the place that takes them (default repo)"`
}

// AdoptOut is the output of resource.adopt.
type AdoptOut struct {
	Kinds  []string `json:"kinds"`
	Fields int      `json:"fields"`
	Store  string   `json:"store"`
	Human  string   `json:"-"`
}

// Text implements op.Texter.
func (o AdoptOut) Text() string { return o.Human }

// RehomeIn is the input of resource.rehome.
type RehomeIn struct {
	In string `json:"in,omitempty" flag:"in" desc:"the place whose resources are checked (default repo)"`
}

// RehomeItem is one thing the place can no longer see.
type RehomeItem struct {
	Resource string `json:"resource"`
	Name     string `json:"name"`
	What     string `json:"what"`
	ID       string `json:"id"`
}

// RehomeOut is the output of resource.rehome.
type RehomeOut struct {
	Clashes []string     `json:"clashes"`
	Missing []RehomeItem `json:"missing"`
	Store   string       `json:"store"`
}

// Text implements op.Texter.
func (o RehomeOut) Text() string {
	var b strings.Builder
	if len(o.Clashes) == 0 && len(o.Missing) == 0 {
		fmt.Fprintf(&b, "✓ every kind and ref of %s is visible from here\n", place(store.Source(o.Store)))
		return b.String()
	}
	for _, c := range o.Clashes {
		b.WriteString("kind clash: " + c + "\n")
	}
	for _, m := range o.Missing {
		fmt.Fprintf(&b, "%s (%s): %s %s is not visible\n", m.Name, m.Resource, m.What, m.ID)
	}
	b.WriteString("nothing written; adopt the kinds (lore resource adopt --from <place>) or repoint the refs (lore resource repoint)\n")
	return b.String()
}

// layerOps are adopt and rehome.
func (o ops) layerOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpResAdopt, Summary: "Move another place's resource kind and field rows into this place (ids kept)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: append(slices.Clone(layerErrors), errs.Unsupported), Confirm: op.ConfirmRule{MayAsk: true}}, o.adopt),
		op.Define(op.Spec{Name: OpResRehome, Summary: "List the kinds and refs a place can no longer see after its chain changed", Permission: op.PermRead, Access: op.AccessRead,
			Errors: append(slices.Clone(readErrors), errs.KindConflict)}, o.rehome),
	}
}

// adopt moves every live kind and kind_fields row of --from into --to,
// keeping ids (R6). Flow: list from's kinds and fields → refuse when a
// kind name is already defined in the target under another id (E_KIND_
// CONFLICT, DECISION: no automatic merge of two definitions) → ask
// (E_NEEDS_CONFIRM) → Move kinds first, then their fields.
func (o ops) adopt(ctx context.Context, in AdoptIn) (AdoptOut, error) {
	if in.From == "" {
		return AdoptOut{}, errs.New(errs.InvalidInput, "resource adopt needs --from <place>")
	}
	mv, ok := o.env.(Mover)
	if !ok {
		return AdoptOut{}, errs.New(errs.Unsupported, "resource adopt needs the engine's move")
	}
	from, to := store.Source(in.From), targetOf(in.To)
	if from == to {
		return AdoptOut{}, errs.New(errs.InvalidInput, "--from and --to name the same place")
	}
	kinds, err := liveRows(ctx, o.env, []store.Source{from}, schema.TableKinds)
	if err != nil {
		return AdoptOut{}, err
	}
	fields, err := liveRows(ctx, o.env, []store.Source{from}, schema.TableKindFields)
	if err != nil {
		return AdoptOut{}, err
	}
	have, err := liveRows(ctx, o.env, []store.Source{to}, schema.TableKinds)
	if err != nil {
		return AdoptOut{}, err
	}
	var clash, names []string
	for _, k := range kinds {
		names = append(names, text(k.Row, kName))
		for _, h := range where(have, map[string]string{kName: text(k.Row, kName)}) {
			if h.Row.ID != k.Row.ID {
				clash = append(clash, fmt.Sprintf("%s (%s here, %s in %s)", text(k.Row, kName), h.Row.ID, k.Row.ID, from))
			}
		}
	}
	if len(clash) > 0 {
		return AdoptOut{}, errs.New(errs.KindConflict, "both places define "+strings.Join(clash, ", ")).
			WithHint("rename one of the kinds first (lore resource kind rename); nothing written")
	}
	if len(kinds) == 0 && len(fields) == 0 {
		return AdoptOut{}, errs.New(errs.NotFound, "no kind or field rows in "+string(from))
	}
	if err = bulk.Ask(ctx, fmt.Sprintf("move %d kinds (%s) and %d field rows from %s into %s?", len(kinds), strings.Join(names, ", "), len(fields), from, place(to))); err != nil {
		return AdoptOut{}, err
	}
	for _, l := range append(kinds, fields...) {
		if _, err = mv.Move(ctx, engine.Ref{Store: l.Src, Table: l.Row.Table, ID: l.Row.ID}, to); err != nil {
			return AdoptOut{}, err
		}
	}
	return AdoptOut{Kinds: append([]string{}, names...), Fields: len(fields), Store: string(to),
		Human: fmt.Sprintf("✓ adopted %d kinds (%s) and %d field rows from %s into %s; ids unchanged\n", len(kinds), strings.Join(names, ", "), len(fields), from, place(to))}, nil
}

// rehome reports what the place's resources can no longer see after its
// chain changed (R6): kind names defined twice in the view, resources
// whose kind is not visible, ref values whose resource is not visible.
// Report only (DECISION: refs carry no stored label yet, so there is
// nothing to repoint by label; repoint stays a separate command).
func (o ops) rehome(ctx context.Context, in RehomeIn) (RehomeOut, error) {
	src := targetOf(in.In)
	out := RehomeOut{Clashes: []string{}, Missing: []RehomeItem{}, Store: string(src)}
	kinds, _, err := o.kindRows(ctx)
	if err != nil {
		return RehomeOut{}, err
	}
	byName := map[string][]located{}
	visible := map[string]located{}
	for _, k := range kinds {
		visible[k.Row.ID.String()] = k
		byName[text(k.Row, kName)] = append(byName[text(k.Row, kName)], k)
	}
	for name, ks := range byName {
		if _, lerr := o.lookupKind(ctx, name); errs.Has(lerr, errs.KindConflict) {
			var where []string
			for _, k := range ks {
				where = append(where, k.Row.ID.String()+" in "+place(k.Src))
			}
			out.Clashes = append(out.Clashes, name+": "+strings.Join(where, ", "))
		}
	}
	slices.Sort(out.Clashes)
	all, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableResources)
	if err != nil {
		return RehomeOut{}, err
	}
	seen := map[string]bool{}
	for _, r := range all {
		seen[r.Row.ID.String()] = true
	}
	for _, r := range all {
		if r.Src != src {
			continue
		}
		item := RehomeItem{Resource: r.Row.ID.String(), Name: text(r.Row, rName)}
		k, ok := visible[text(r.Row, rKind)]
		if !ok {
			item.What, item.ID = "kind", text(r.Row, rKind)
			out.Missing = append(out.Missing, item)
			continue
		}
		raw, ferr := o.rawFieldsOf(ctx, k)
		if ferr != nil {
			return RehomeOut{}, ferr
		}
		stored, verr := decodeValues(r)
		if verr != nil {
			return RehomeOut{}, verr
		}
		for _, f := range raw {
			if text(f.Row, fType) != string(value.KindRef) || schema.ExtendsOf(f.Row) != "" {
				continue
			}
			var id string
			if json.Unmarshal(stored.vals[f.Row.ID.String()], &id) != nil || id == "" || seen[id] {
				continue
			}
			item.What, item.ID = "ref "+text(f.Row, kName)+" →", id
			out.Missing = append(out.Missing, item)
		}
	}
	return out, nil
}
