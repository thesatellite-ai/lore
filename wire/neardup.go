package wire

// neardup.go — the near-duplicates a move into a folder leaves (devco
// D-724): store adopt (and --move) keeps every id, so a rule the folder
// already held by hand and the repo's copy of it both stay. The move lists
// each pair (logic/neardup decides what counts) with the commands that
// compare them and drop one; it never merges (which wording, status and
// relations win is the person's call).

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/neardup"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Text fields the detector reads.
const (
	nearDupTitle = "title"
	nearDupBody  = "body"
)

// nearDuplicates pairs each row the changes bring into a folder (a row the
// folder did not hold) with a live row of the folder, have, it repeats.
func nearDuplicates(changes []store.Change, have []store.Row) []opsstores.NearDuplicate {
	held := make(map[store.Key]bool, len(have))
	for _, h := range have {
		held[h.Key()] = true
	}
	moving := make(map[store.Key]bool, len(changes))
	var incoming []neardup.Item
	for _, c := range changes {
		k := c.Row.Key()
		moving[k] = true
		if held[k] {
			continue // a newer copy of a row the folder holds: one row, not two
		}
		if it, ok := nearDupItem(c.Row); ok {
			incoming = append(incoming, it)
		}
	}
	existing := make([]neardup.Item, 0, len(have))
	for _, h := range have {
		if moving[h.Key()] {
			continue
		}
		if it, ok := nearDupItem(h); ok {
			existing = append(existing, it)
		}
	}
	reg, rerr := builtinRegistry()
	matches := neardup.Find(incoming, existing)
	out := make([]opsstores.NearDuplicate, 0, len(matches))
	for _, m := range matches {
		d := opsstores.NearDuplicate{Table: m.New.Table, ID: m.New.ID, Existing: m.Existing.ID, Title: m.New.Title, Why: string(m.Why), Percent: m.Percent}
		d.Kind = tableNoun(row.Table(m.New.Table))
		if rerr == nil {
			if def, ok := reg.Table(row.Table(m.New.Table)); ok && def.Noun != "" {
				d.Kind = def.Noun
			}
		}
		out = append(out, d)
	}
	return out
}

// nearDupItem is a live row's title and body; false for a deleted or
// trashed row, or one that does not decode.
func nearDupItem(sr store.Row) (neardup.Item, bool) {
	if sr.Deleted {
		return neardup.Item{}, false
	}
	r, err := sr.Decode()
	if err != nil || r.Deleted || trashed(r) {
		return neardup.Item{}, false
	}
	text := func(f string) string {
		if v, ok := r.Fields[f]; ok && v != nil {
			return v.String()
		}
		return ""
	}
	body := text(nearDupBody)
	it := neardup.Item{Table: r.Table.String(), ID: r.ID.String(), Title: neardup.Headline(text(nearDupTitle), text(schema.FieldSummary), body), Body: body}
	return it, it.Title != "" || it.Body != ""
}

// tableNoun is the command noun of a table whose noun is its plain
// singular (rules → rule, memories → memory); "" for one spelled
// otherwise (an underscore; a declared schema.Noun is read above), so a
// printed command never names a command that does not exist.
func tableNoun(t row.Table) string {
	name := string(t)
	switch {
	case strings.Contains(name, "_"):
		return ""
	case strings.HasSuffix(name, "ies"):
		return strings.TrimSuffix(name, "ies") + "y"
	case strings.HasSuffix(name, "s"):
		return strings.TrimSuffix(name, "s")
	}
	return ""
}

// MoveNearDuplicates implements opsrow.NearDuplicateFinder (and
// opsmemory's): the rows a move (`lore move --from`, `lore move --id`,
// `lore memory move`; devco D-854) would bring into to that repeat a live
// row to already holds, by the rules store adopt lists them by (devco
// D-724). Read before the move; nothing is written. Only the tables of
// the moving rows are read (a pair is always one table).
func (a *App) MoveNearDuplicates(ctx context.Context, moving []row.Row, to store.Source) ([]opsstores.NearDuplicate, error) {
	if len(moving) == 0 {
		return nil, nil
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, err
	}
	to = eng.StoreName(to)
	changes := make([]store.Change, 0, len(moving))
	tables := map[row.Table]bool{}
	for _, r := range moving {
		sr, ferr := store.FromRow(r)
		if ferr != nil {
			return nil, errs.Wrap(errs.Internal, "move near-duplicates: "+r.ID.String(), ferr)
		}
		changes = append(changes, store.Change{Row: sr})
		tables[r.Table] = true
	}
	var have []store.Row
	for _, t := range slices.Sorted(maps.Keys(tables)) {
		rows, lerr := eng.List(ctx, to, t)
		if lerr != nil {
			return nil, lerr
		}
		for _, r := range rows {
			sr, ferr := store.FromRow(r)
			if ferr != nil {
				return nil, errs.Wrap(errs.Internal, "move near-duplicates: "+r.ID.String()+" in "+string(to), ferr)
			}
			have = append(have, sr)
		}
	}
	return nearDuplicates(changes, have), nil
}

// TaskSubtree implements opsrow.SubtreeReader: the sub-tasks a one-row
// `lore move --id <task> --to <to> --with-children` moves with id
// (engine.TaskSubtree), read before the move so its near-duplicate check
// covers them (devco D-854). Nothing is written.
func (a *App) TaskSubtree(ctx context.Context, to store.Source, id ids.AnyID) ([]ids.AnyID, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, err
	}
	return eng.TaskSubtree(ctx, to, id)
}
