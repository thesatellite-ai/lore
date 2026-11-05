package settings

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
)

// metaRow is one data row that carries metadata, with its entries decoded
// (the maintenance commands of METADATA 3.8, 3.18, 3.19 read every row's
// `meta` column: forget, --unused, lint, --where, export, merge-into).
type metaRow struct {
	located
	entries map[ids.ID[ids.MetaKey]]meta.Entry
}

// metaDataTables are the tables whose rows take metadata (engine.AcceptsMeta),
// in a stable order.
func metaDataTables() []row.Table {
	var out []row.Table
	for _, t := range schema.Builtin() {
		if engine.AcceptsMeta(t.Name) {
			out = append(out, t.Name)
		}
	}
	slices.Sort(out)
	return out
}

// metaRows reads every row with metadata in every attached store. Archived
// rows count (3.19: archived rows are in use). An entry that does not
// decode is skipped here; `meta lint` reports it.
func (o ops) metaRows(ctx context.Context) ([]metaRow, error) { return o.rowsOf(ctx, false) }

// rowsOf is metaRows; all keeps rows without metadata too (a --where
// filter such as `jira!?` must see them).
func (o ops) rowsOf(ctx context.Context, all bool) ([]metaRow, error) {
	var out []metaRow
	for _, t := range metaDataTables() {
		rows, err := liveRows(ctx, o.env, o.env.Stores(), t)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if len(r.Row.Meta) == 0 && !all {
				continue
			}
			mr := metaRow{located: r, entries: map[ids.ID[ids.MetaKey]]meta.Entry{}}
			for kid, j := range r.Row.Meta {
				if en, derr := meta.DecodeEntry(j); derr == nil {
					mr.entries[kid] = en
				}
			}
			out = append(out, mr)
		}
	}
	return out, nil
}

// trasher is the optional Env upgrade the used-key check needs: the rows
// in the trash of every attached store (wire.App has it).
type trasher interface {
	Trashed(ctx context.Context) ([]engine.Trashed, error)
}

// usedRows is metaRows plus the trashed rows that carry metadata (OV58):
// a key or value used only by a row in the trash is still used, because
// restoring that row would bring back an id nothing defines. An Env
// without a trash reads live rows only.
func (o ops) usedRows(ctx context.Context) ([]metaRow, error) {
	out, err := o.metaRows(ctx)
	if err != nil {
		return nil, err
	}
	tr, ok := upgrade.As[trasher](o.env)
	if !ok {
		return out, nil
	}
	all, err := tr.Trashed(ctx)
	if err != nil {
		return nil, err
	}
	for _, it := range all {
		if len(it.Row.Meta) == 0 || !engine.AcceptsMeta(it.Row.Table) {
			continue
		}
		mr := metaRow{located: located{Src: it.Store, Row: it.Row}, entries: map[ids.ID[ids.MetaKey]]meta.Entry{}}
		for kid, j := range it.Row.Meta {
			if en, derr := meta.DecodeEntry(j); derr == nil {
				mr.entries[kid] = en
			}
		}
		out = append(out, mr)
	}
	return out, nil
}

// keyUse is how a key is used: the rows (by table) that carry it.
type keyUse struct {
	rows    int
	byTable map[row.Table]int
	values  map[string]bool
}

// usage counts, per key id, the rows that carry it.
func usage(rows []metaRow) map[string]*keyUse {
	out := map[string]*keyUse{}
	for _, r := range rows {
		for kid, en := range r.entries {
			u := out[kid.String()]
			if u == nil {
				u = &keyUse{byTable: map[row.Table]int{}, values: map[string]bool{}}
				out[kid.String()] = u
			}
			u.rows++
			u.byTable[r.Row.Table]++
			for _, s := range meta.Strings(en.V) {
				u.values[s] = true
			}
		}
	}
	return out
}

// tableCounts renders "38 tasks, 2 memories".
func (u *keyUse) tableCounts() string {
	if u == nil {
		return ""
	}
	ts := make([]string, 0, len(u.byTable))
	for t := range u.byTable {
		ts = append(ts, string(t))
	}
	slices.Sort(ts)
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		parts = append(parts, strconv.Itoa(u.byTable[row.Table(t)])+" "+t)
	}
	return strings.Join(parts, ", ")
}
