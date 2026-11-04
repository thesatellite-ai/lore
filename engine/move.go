package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// Keys of one move entry in _o (PROVENANCE_DESIGN 5.4: {at, by,
// moved_from, moved_to}). The entries are kept in the `moves` list of _o
// because row.Row has no bookkeeping field of its own (spec-deltas.md);
// moved_from / moved_to name attached store names until store ids are
// carried by Attached.
const (
	originMoves  = "moves"
	moveAt       = "at"
	moveBy       = "by"
	moveFrom     = "moved_from"
	moveTo       = "moved_to"
	listOpen     = "["
	listClose    = "]"
	listSep      = ","
	emptyJSONObj = "{}"
)

// Move moves one live row to the attached store to, keeping its id (PROV5):
// the row is written to to (validated, ref-checked and secret-scanned under
// to's policy, like a create there) with a `moves` entry appended to its _o,
// then a tombstone is written where it was. ref.Store, when set, names the
// source; otherwise every attached store except to is searched.
//
// Refusals (nothing written): to not attached or read-only; the row is
// already in to; to holds a tombstone of it (rows never come back to a
// place they left, spec 11 rule 3); to is a client repo and the row's text
// holds secret references (BATCH 5, the references are listed).
//
// Interrupted after the first write, the row is in both stores; running
// Move again finds the copy in to and only writes the source tombstone.
func (e *Engine) Move(ctx context.Context, ref Ref, to store.Source) (Written, error) {
	cl := e.callOf(ctx)
	dst, err := e.writable(to)
	if err != nil {
		return Written{}, err
	}
	src, cur, err := e.locateExcept(ctx, ref, to)
	if err != nil {
		return Written{}, err
	}
	srcSt, err := e.writable(src)
	if err != nil {
		return Written{}, err
	}
	if cur.Deleted {
		return Written{}, errs.New(errs.NotFound, "engine: "+cur.ID.String()+" is deleted")
	}
	def, err := e.tableDef(cur.Table)
	if err != nil {
		return Written{}, err
	}
	actor := e.actorOf(cl)
	there, err := dst.Backend.Get(ctx, cur.Table, cur.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		there = store.Row{}
	case err != nil:
		return Written{}, err
	case there.Deleted:
		return Written{}, errs.New(errs.Unsupported, "engine: "+cur.ID.String()+" was in store "+string(to)+" before and left it; a row never returns to a place it left").
			WithHint("keep it in " + string(src))
	}
	var moved row.Row
	if there.ID.IsZero() {
		if dst.Client {
			if err := refuseSecretRefs(cur); err != nil {
				return Written{}, err
			}
		}
		names := slices.Sorted(maps.Keys(cur.Fields))
		if err = e.checkRefs(ctx, to, def, cur.Fields, names); err != nil {
			return Written{}, err
		}
		sc, err := e.screen(ctx, cl, to, cur.ID, cur.Fields, names)
		if err != nil {
			return Written{}, err
		}
		moved = cur
		moved.Fields = sc.fields
		moved.HLC = e.clock.Now(cur.HLC)
		moved.UpdatedAt, moved.UpdatedBy = moved.HLC.Wall, actor
		if !dst.Client {
			if moved.Origin, err = withMove(cur.Origin, moved.HLC, actor.String(), string(src), string(to)); err != nil {
				return Written{}, err
			}
		}
		got, err := e.put(ctx, dst, moved, hlc.HLC{})
		if err != nil {
			return Written{}, err
		}
		ev := Event{Op: cl.op, Action: ActionCreate, Store: to, Table: cur.Table, ID: cur.ID, HLC: moved.HLC, Actor: actor,
			SecretsAccepted: sc.accepted, SecretsRedacted: sc.redacted}
		if err := e.finish(ctx, to, []store.Row{got.doc}, ev); err != nil {
			return Written{}, err
		}
	} else if moved, err = there.Decode(); err != nil {
		return Written{}, err
	}
	h := e.clock.Now(cur.HLC)
	tomb := row.Row{Table: cur.Table, ID: cur.ID, HLC: h, Deleted: true, UpdatedAt: h.Wall, UpdatedBy: actor}
	got, err := e.put(ctx, srcSt, tomb, cur.HLC)
	if err != nil {
		return Written{Row: moved}, errs.Wrap(errs.CodeOf(err), "engine: "+cur.ID.String()+" is now in "+string(to)+" but its old copy in "+string(src)+" could not be removed", err).
			WithHint("run the same move again: it only removes the old copy")
	}
	ev := Event{Op: cl.op, Action: ActionDelete, Store: src, Table: cur.Table, ID: cur.ID, HLC: h, Actor: actor}
	return Written{Row: moved}, e.finish(ctx, src, []store.Row{got.doc}, ev)
}

// locateExcept finds ref's row in ref.Store, or in every attached store
// but skip (exactly one may hold it).
func (e *Engine) locateExcept(ctx context.Context, ref Ref, skip store.Source) (store.Source, row.Row, error) {
	if ref.Store != "" {
		return e.locate(ctx, ref)
	}
	var found []store.Source
	var got row.Row
	for _, name := range e.order {
		if name == skip {
			continue
		}
		src, r, err := e.locate(ctx, Ref{Store: name, Table: ref.Table, ID: ref.ID})
		if errs.Has(err, errs.NotFound) {
			continue
		}
		if err != nil {
			return "", row.Row{}, err
		}
		found, got = append(found, src), r
	}
	switch len(found) {
	case 0:
		return "", row.Row{}, errs.Wrap(errs.NotFound, "engine: no row "+ref.ID.String()+" outside "+string(skip), store.ErrNotFound)
	case 1:
		return found[0], got, nil
	}
	parts := make([]string, len(found))
	for i, f := range found {
		parts[i] = string(f)
	}
	return "", row.Row{}, errs.New(errs.AmbiguousID, "engine: "+ref.ID.String()+" is in "+strings.Join(parts, " and "))
}

// refuseSecretRefs refuses a row whose text holds ${secret:…} references
// (BATCH 5: a client repo must never receive them), listing each.
func refuseSecretRefs(r row.Row) error {
	var found []string
	for _, name := range slices.Sorted(maps.Keys(r.Fields)) {
		for _, t := range texts(r.Fields[name]) {
			for _, ref := range refs.References(t) {
				if ref.Scheme == refs.SchemeSecret {
					found = append(found, name+": "+ref.String())
				}
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	return errs.New(errs.Unsupported, "engine: "+r.ID.String()+" holds secret references and cannot move into a client repo: "+strings.Join(found, ", ")).
		WithHint("remove the references from the row first (BATCH 5)")
}

// texts returns the text of a value: a text, a json value's raw text, or
// every text item of a list.
func texts(v value.Value) []string {
	switch x := v.(type) {
	case value.Text:
		return []string{x.String()}
	case value.JSON:
		return []string{x.String()}
	case value.List:
		var out []string
		for _, it := range x.Items() {
			if t, ok := it.(value.Text); ok {
				out = append(out, t.String())
			}
		}
		return out
	}
	return nil
}

// withMove returns origin with one more entry in its `moves` list.
func withMove(origin value.JSON, at hlc.HLC, by, from, to string) (value.JSON, error) {
	raw := []byte(emptyJSONObj)
	if !origin.IsZero() {
		raw = origin.Raw()
	}
	ms, err := canon.Members(raw)
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "engine: read _o", err)
	}
	entry, err := canon.Object([]canon.Member{
		{Key: moveAt, Raw: canon.String(value.DateTimeFrom(at.Wall).String())},
		{Key: moveBy, Raw: canon.String(by)},
		{Key: moveFrom, Raw: canon.String(from)},
		{Key: moveTo, Raw: canon.String(to)},
	})
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "engine: build a move entry", err)
	}
	var list [][]byte
	keep := ms[:0]
	for _, m := range ms {
		if m.Key == originMoves {
			if list, err = canon.Elements(m.Raw); err != nil {
				return value.JSON{}, errs.Wrap(errs.Internal, "engine: _o.moves is not a list", err)
			}
			continue
		}
		keep = append(keep, m)
	}
	list = append(list, entry)
	parts := make([]string, len(list))
	for i, b := range list {
		parts[i] = string(b)
	}
	keep = append(keep, canon.Member{Key: originMoves, Raw: []byte(listOpen + strings.Join(parts, listSep) + listClose)})
	obj, err := canon.Object(keep)
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "engine: build _o", err)
	}
	return value.JSONOf(obj)
}
