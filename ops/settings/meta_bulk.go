package settings

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/filter"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/schema"
)

// bulkSample is how many row ids a dry run prints before "…".
const bulkSample = 20

// metaMatcher binds --where to the metadata keys every attached store
// defines (XQ7, the one filter grammar): a key part names a metadata key
// (bare or meta.<key>); its type gives the comparison kind and its allowed
// values the enum order. Retired keys are not names any more (3.12).
func (o ops) metaMatcher(ctx context.Context, where []string) (filter.Matcher, error) {
	f, err := filter.Parse(where)
	if err != nil {
		return filter.Matcher{}, err
	}
	keys, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableMetaKeys)
	if err != nil {
		return filter.Matcher{}, err
	}
	return f.Bind(func(k filter.Key) (filter.Field, error) {
		var names []string
		for _, kr := range keys {
			if text(kr.Row, mMergedInto) != "" {
				continue
			}
			names = append(names, text(kr.Row, mName))
			if text(kr.Row, mName) != k.Name {
				continue
			}
			typ, terr := keyType(kr)
			if terr != nil {
				return filter.Field{}, terr
			}
			vs, verr := o.metaValues(ctx, kr)
			if verr != nil {
				return filter.Field{}, verr
			}
			fd := filter.Field{Kind: typ.Kind, Elem: typ.Elem, Values: valueNames(vs)}
			if typ.Kind == value.KindJSON {
				fd.Kind = value.KindText
			}
			return fd, nil
		}
		slices.Sort(names)
		return filter.Field{}, errs.New(errs.MetaUnknown, "--where: no metadata key "+quote(k.Name)+" (keys: "+strings.Join(names, ", ")+")")
	})
}

// metaGetter reads a row's values for the matcher, by key name (allowed
// values by their names).
func metaGetter(r metaRow) filter.Getter {
	return func(k filter.Key) []string {
		for _, en := range r.entries {
			if en.Name == k.Name {
				return meta.Names(en)
			}
		}
		return nil
	}
}

// metaHits are the rows matching where, sorted by id.
func (o ops) metaHits(ctx context.Context, where []string) ([]metaRow, error) {
	m, err := o.metaMatcher(ctx, where)
	if err != nil {
		return nil, err
	}
	rows, err := o.rowsOf(ctx, true)
	if err != nil {
		return nil, err
	}
	var hits []metaRow
	for _, r := range rows {
		if m.Match(metaGetter(r)) {
			hits = append(hits, r)
		}
	}
	slices.SortFunc(hits, func(a, b metaRow) int { return strings.Compare(a.Row.ID.String(), b.Row.ID.String()) })
	return hits, nil
}

// metaBulk runs one metadata change on every hit in ONE batch of kind meta
// (3.19, XQ1): a dry run lists the rows; otherwise above batch.preview_rows
// nothing is written without --yes (XQ2), then each row is written with
// the batch on its audit event so `lore batch undo` reverses it.
func (o ops) metaBulk(ctx context.Context, what string, hits []metaRow, dryRun bool, write func(metaRow) engine.MetaWrite) (RowOut, error) {
	if len(hits) == 0 {
		return RowOut{Human: "no rows match; nothing written\n"}, nil
	}
	items := make([]bulk.Item, 0, len(hits))
	counts := map[string]int{}
	for _, h := range hits {
		items = append(items, bulk.Item{ID: h.Row.ID.String()})
		counts[string(h.Row.Table)]++
	}
	var tabs []string
	for t, n := range counts {
		tabs = append(tabs, t+" "+strconv.Itoa(n))
	}
	slices.Sort(tabs)
	if dryRun {
		ids := make([]string, 0, bulkSample)
		for i, it := range items {
			if i == bulkSample {
				ids = append(ids, "…")
				break
			}
			ids = append(ids, it.ID)
		}
		return RowOut{Human: fmt.Sprintf("would %s on %d rows (%s): %s\ndry run: nothing written\n", what, len(hits), strings.Join(tabs, ", "), strings.Join(ids, ", "))}, nil
	}
	if err := bulk.Preview(ctx, o.env, hits[0].Src, "meta "+what, items); err != nil {
		return RowOut{}, err
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindMeta)
	if err != nil {
		return RowOut{}, err
	}
	var warns []string
	for i, h := range hits {
		w, uerr := o.env.Update(engine.WithMeta(bctx, write(h)), engine.Ref{ID: h.Row.ID}, map[string]value.Value{})
		if uerr != nil {
			return RowOut{}, errs.Wrap(errs.CodeOf(uerr), fmt.Sprintf("meta %s: stopped after %d of %d rows (batch %s; undo: lore batch undo %s)", what, i, len(hits), bid, bid), uerr)
		}
		for _, x := range w.Warnings {
			if !slices.Contains(warns, x.Message) {
				warns = append(warns, x.Message)
			}
		}
	}
	human := fmt.Sprintf("%s on %d rows; batch %s (undo: lore batch undo %s)\n", what, len(hits), bid, bid)
	for _, x := range warns {
		human += x + "\n"
	}
	return RowOut{ID: bid.String(), Human: human}, nil
}

// metaSetWhere is `lore meta set --where '<filter>' k=v …` (3.19).
func (o ops) metaSetWhere(ctx context.Context, in MetaSetIn) (RowOut, error) {
	f := engine.MetaFlags{MetaJSON: in.JSON, MetaStdin: in.Stdin}
	for _, p := range []string{in.ID, in.Pair} {
		if p != "" {
			f.Meta = append(f.Meta, p)
		}
	}
	f.Meta = append(f.Meta, in.More...)
	if f.Empty() {
		return RowOut{}, errs.New(errs.InvalidInput, "meta set --where: nothing to set").WithHint("lore meta set --where 'client=acme' key=value")
	}
	// parse once (stdin is read once), bind per row below
	proto, err := f.Write("", ids.AnyID{}, nil)
	if err != nil {
		return RowOut{}, err
	}
	hits, err := o.metaHits(ctx, in.Where)
	if err != nil {
		return RowOut{}, err
	}
	var names []string
	for _, t := range append(slices.Clone(proto.Set), proto.JSON...) {
		names = append(names, t.Name())
	}
	return o.metaBulk(ctx, "set "+strings.Join(names, ", "), hits, in.DryRun, func(h metaRow) engine.MetaWrite {
		mw := proto
		mw.ID = h.Row.ID
		return mw
	})
}

// metaUnsetWhere is `lore meta unset --where '<filter>' <key>…` (3.19):
// only rows that carry a named key are changed.
func (o ops) metaUnsetWhere(ctx context.Context, in MetaUnsetIn) (RowOut, error) {
	var keys []string
	for _, k := range append([]string{in.ID, in.Key}, in.More...) {
		if k != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return RowOut{}, errs.New(errs.InvalidInput, "meta unset --where: name the key").WithHint("lore meta unset --where 'client?' client --dry-run")
	}
	all, err := o.metaHits(ctx, in.Where)
	if err != nil {
		return RowOut{}, err
	}
	var hits []metaRow
	for _, h := range all {
		has := false
		for _, en := range h.entries {
			for _, k := range keys {
				if en.Name == meta.TopSegment(k) {
					has = true
				}
			}
		}
		if has {
			hits = append(hits, h)
		}
	}
	return o.metaBulk(ctx, "remove "+strings.Join(keys, ", "), hits, in.DryRun, func(h metaRow) engine.MetaWrite {
		var mine []string
		for _, en := range h.entries {
			for _, k := range keys {
				if en.Name == meta.TopSegment(k) && !slices.Contains(mine, k) {
					mine = append(mine, k)
				}
			}
		}
		return engine.MetaWrite{ID: h.Row.ID, Unset: mine}
	})
}
