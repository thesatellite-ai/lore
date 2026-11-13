// Package metalist binds the one metadata filter (logic/metafilter,
// METADATA 3.7) to the entity list commands: the shared --meta / --place /
// --sort flags, the visible key registry read from every attached store,
// and the selection of rows. Every list path calls Select, so a filter
// means the same on every noun.
//
// Flow:
//
//	<noun> list ─ rows (backend) ─ Select(flags)
//	   └ Keys(env): meta_keys + meta_values of every store
//	   └ metafilter.Compile ─ per row: quarantined? → "not checked" note
//	                         └ Match → keep, skipped notes ─ Compare → order
package metalist

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/trash"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/metafilter"
	"github.com/thesatellite-ai/lore/logic/sortspec"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Flags are the shared list flags (3.7, XQ6, XQ10). A list input embeds
// it (no json tag). The json name of --meta is meta_where, never meta:
// engine.bindInputMeta reads an input's "meta" field as a write.
type Flags struct {
	Meta  []string `json:"meta_where,omitempty" flag:"meta" desc:"filter by metadata: key=value, key!=value, key~=text, key>n, key?, key!? (repeatable, all must match)"`
	Place string   `json:"place,omitempty" flag:"place" desc:"narrow --meta names to one place: repo, project, org or a scope label (project:acme-hub)"`
	Sort  string   `json:"sort,omitempty" flag:"sort" desc:"sort by a field (created_at, updated_at or a field of the rows) or a metadata key (meta.<key> names the key), with :asc or :desc; rows without it last"`
	// Archived keeps archived rows in the list and in --meta filters
	// (METADATA 3.19, OV30-2: every list hides archived rows unless
	// --archived; Select applies it, so every noun list behaves the same).
	Archived bool `json:"archived,omitempty" flag:"archived" desc:"include archived rows (also in --meta filters)"`
	Trash
	// Audit is --created-by / --updated-by (INTEG-AUDIT-1, audit.go).
	Audit
}

// Trash is the pair of trash flags every noun list shares (DELETION 3.2:
// a deleted row is invisible to every read but the trash commands and
// these flags; trashed rows are marked). Flags embeds it; a list with its
// own --meta / --sort (task list, resource list) embeds only this (no
// json tag), so the flags spell and behave the same everywhere.
type Trash struct {
	IncludeDeleted bool `json:"include_deleted,omitempty" flag:"include-deleted" desc:"also list rows in the trash (marked in the trash)"`
	DeletedOnly    bool `json:"deleted_only,omitempty" flag:"deleted-only" desc:"list only rows in the trash"`
}

// Any reports whether either trash flag is set.
func (f Trash) Any() bool { return f.IncludeDeleted || f.DeletedOnly }

// Deleted returns ctx whose list of table follows --include-deleted /
// --deleted-only (engine.WithDeleted; --deleted-only wins). Lookups of
// other tables made on the way stay live.
func (f Trash) Deleted(ctx context.Context, table row.Table) context.Context {
	switch {
	case f.DeletedOnly:
		return engine.WithDeleted(ctx, engine.DeletedOnly, table)
	case f.IncludeDeleted:
		return engine.WithDeleted(ctx, engine.DeletedInclude, table)
	}
	return ctx
}

// TrashNotes is the note that marks the listed rows in the trash (with
// --include-deleted / --deleted-only only; a list that adds trashed rows
// by its own flag marks them itself).
func (f Trash) TrashNotes(rows []row.Row) []string {
	if !f.Any() {
		return nil
	}
	var in []string
	for _, r := range rows {
		if trash.Is(r) {
			in = append(in, r.ID.String())
		}
	}
	if len(in) == 0 {
		return nil
	}
	return []string{"in the trash: " + strings.Join(in, ", ") + " (lore trash show --id <id>; lore trash restore --id <id>)"}
}

// Empty reports that no flag was given (the list is unchanged).
func (f Flags) Empty() bool { return len(f.Meta) == 0 && f.Place == "" && f.Sort == "" }

// Lister is the part of the ops Env Select needs.
type Lister interface {
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	Stores() []store.Source
}

// meta_keys / meta_values fields read here (schema.Builtin; engine/meta.go
// reads the same names).
const (
	fName, fScope, fType, fMerged = "name", "scope", "type", "merged_into"
	fKeyID, fLabel                = "key_id", "label"
	fSensitive                    = "sensitive"
	fArchivedAt                   = "archived_at" // every archivable table's column
)

// Keys reads the visible key definitions of every attached store.
func Keys(ctx context.Context, env Lister) ([]metafilter.Key, error) {
	var out []metafilter.Key
	for _, src := range engine.ReadStoresOf(env) {
		keys, err := env.List(ctx, src, schema.TableMetaKeys)
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 {
			continue
		}
		vals, err := env.List(ctx, src, schema.TableMetaValues)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if k.Deleted {
				continue
			}
			id, perr := ids.Parse[ids.MetaKey](k.ID.String())
			if perr != nil {
				continue // a foreign id: meta lint reports it
			}
			t, terr := meta.ParseType(text(k, fType))
			if terr != nil {
				continue
			}
			d := metafilter.Key{ID: id, Name: text(k, fName), Scope: text(k, fScope), Type: t, Retired: text(k, fMerged) != "", Sensitive: text(k, fSensitive) == "true"}
			for _, v := range vals {
				if !v.Deleted && text(v, fKeyID) == k.ID.String() {
					d.Values = append(d.Values, metafilter.Value{Name: text(v, fName), Label: text(v, fLabel)})
				}
			}
			out = append(out, d)
		}
	}
	return out, nil
}

func text(r row.Row, f string) string {
	if v, ok := r.Fields[f]; ok && v != nil && v.Kind() != value.KindNull {
		return v.String()
	}
	return ""
}

// Result is a selection: Order indexes the input rows in output order;
// Notes are the warning, "skipped" and "not checked" lines; Columns the
// filtered key names (3.7).
type Result struct {
	Order   []int
	Notes   []string
	Columns []string
}

// Select filters and sorts rows by f. Archived rows are left out unless
// --archived (OV30-2, every list). With no other flag it returns the
// remaining indexes in input order and reads nothing.
func Select(ctx context.Context, env Lister, f Flags, rows []row.Row) (Result, error) {
	who, err := f.Audit.matcher()
	if err != nil {
		return Result{}, err
	}
	all := make([]int, 0, len(rows))
	for i, r := range rows {
		if !f.Archived && text(r, fArchivedAt) != "" {
			continue // 3.19 / OV30-2: archived rows only with --archived
		}
		if !who.keep(ctx, r) {
			continue // INTEG-AUDIT-1 --created-by / --updated-by
		}
		all = append(all, i)
	}
	byField, isField, err := fieldSort(f.Sort, rows)
	if err != nil {
		return Result{}, err
	}
	if isField {
		f.Sort = "" // the metadata plan sorts nothing; the field order is applied last
	}
	if f.Empty() {
		if isField {
			sortByField(all, rows, byField)
		}
		return Result{Order: all}, nil
	}
	keys, err := Keys(ctx, env)
	if err != nil {
		return Result{}, err
	}
	aliases, err := ScopeAliases(ctx, env) // 3.6 meta.scope_aliases (FA-META-12)
	if err != nil {
		return Result{}, err
	}
	p, warnings, err := metafilter.Compile(meta.ExpandAliases(f.Meta, aliases), f.Place, f.Sort, keys)
	if err != nil {
		return Result{}, err
	}
	res := Result{Notes: warnings, Columns: p.Columns}
	var skipped []string
	var unchecked []string
	for _, i := range all {
		r := rows[i]
		if len(f.Meta) > 0 && len(metafilter.Quarantine(r.Meta)) > 0 {
			// 3.13: quarantined metadata is kept out of filters.
			unchecked = append(unchecked, r.ID.String())
			continue
		}
		ok, note := p.Match(r.ID.String(), r.Meta)
		if note != "" {
			skipped = append(skipped, string(note))
		}
		if ok {
			res.Order = append(res.Order, i)
		}
	}
	slices.SortStableFunc(res.Order, func(a, b int) int { return p.Compare(rows[a].Meta, rows[b].Meta) })
	if isField {
		sortByField(res.Order, rows, byField)
	}
	res.Notes = append(res.Notes, skipped...)
	for _, id := range unchecked {
		res.Notes = append(res.Notes, "note: 1 row not checked: "+id+" has quarantined metadata")
	}
	// 3.7 "each filtered key becomes a column" (FA-META-5): every list
	// prints its own row shape, so the filtered keys' values follow the
	// rows as one line per listed row, sensitive values masked (3.20).
	sens := map[string]bool{}
	for _, k := range keys {
		if k.Sensitive {
			sens[k.ID.String()] = true
		}
	}
	for _, i := range res.Order {
		if line := columnLine(rows[i], p.Columns, sens); line != "" {
			res.Notes = append(res.Notes, line)
		}
	}
	if len(f.Meta) > 0 && filtersSensitive(keys, p.Columns) {
		res.Notes = append(res.Notes, noteSensitiveFilter)
	}
	return res, nil
}

// noteSensitiveFilter is METADATA 3.20's line under a list whose --meta
// filter named a sensitive key: the filter compared the stored values,
// the list prints them masked.
const noteSensitiveFilter = "note: filtered on a sensitive key; values are masked"

// filtersSensitive reports whether a filtered column (a key name or key
// id, with any scope prefix, type suffix or dotted field; columnKey) is a
// sensitive key.
func filtersSensitive(keys []metafilter.Key, cols []string) bool {
	for _, c := range cols {
		top := columnKey(c)
		for _, k := range keys {
			if k.Sensitive && (k.Name == top || k.ID.String() == top) {
				return true
			}
		}
	}
	return false
}

// columnLine is "meta: <id>  key=value  key=value" for the filtered key
// names cols ("" when cols is empty). A column names a key as the filter
// did: a scope prefix and a type suffix are dropped, a dotted name shows
// its top key's value, a key id matches by id; a row without the key
// shows "key=-".
func columnLine(r row.Row, cols []string, sens map[string]bool) string {
	if len(cols) == 0 {
		return ""
	}
	parts := []string{"meta: " + r.ID.String()}
	for _, c := range cols {
		name := columnKey(c)
		shown := "-"
		for kid, j := range r.Meta {
			en, err := meta.DecodeEntry(j)
			if err != nil || (en.Name != name && kid.String() != name) {
				continue
			}
			en.Sensitive = en.Sensitive || sens[kid.String()]
			shown = meta.Show(en)
			break
		}
		parts = append(parts, c+"="+shown)
	}
	return strings.Join(parts, "  ")
}

// columnKey is the top key name (or key id) of a filter's key part:
// "project:deploy.env:text" → "deploy".
func columnKey(c string) string {
	segs := strings.Split(c, ":")
	if len(segs) > 1 {
		if _, err := meta.ParseType(segs[len(segs)-1]); err == nil {
			segs = segs[:len(segs)-1]
		}
	}
	last := segs[len(segs)-1]
	if strings.HasPrefix(last, "mky_") {
		return strings.SplitN(last, ".", 2)[0]
	}
	return meta.TopSegment(strings.SplitN(last, "$", 2)[0])
}

// Envelope fields every row has outside Fields, sortable by name.
const (
	sortCreatedAt = "created_at"
	sortUpdatedAt = "updated_at"
)

// metaSortPrefix names a metadata key explicitly in --sort (meta.<key>).
const metaSortPrefix = "meta."

// oldDescPrefix is v0.1.17's `--sort -key`, refused by sortspec (XQ10).
const oldDescPrefix = "-"

// fieldSort reads --sort as a field sort (CLI_REFERENCE 2.2: "a field or a
// metadata key"; decision G-CONTENT-1). The key is a field when it is
// created_at / updated_at or a field some listed row carries; a key named
// meta.<key>, or one no row carries as a field, stays a metadata sort for
// metafilter (so a name that is both is the field unless spelled meta.).
// ok is false for "" and for metadata sorts.
func fieldSort(raw string, rows []row.Row) (sortspec.Spec, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, metaSortPrefix) {
		return sortspec.Spec{}, false, nil
	}
	if _, err := sortspec.Parse([]string{raw}); err != nil && strings.HasPrefix(raw, oldDescPrefix) {
		return sortspec.Spec{}, false, err // XQ10: `-key` fails with "use <key>:desc" for fields and keys alike
	}
	key, _, _ := strings.Cut(raw, ":")
	if !isFieldKey(key, rows) {
		return sortspec.Spec{}, false, nil
	}
	specs, err := sortspec.Parse([]string{raw})
	if err != nil || len(specs) != 1 {
		return sortspec.Spec{}, false, err
	}
	return specs[0], true, nil
}

// isFieldKey reports whether key is an envelope time or a field of a row.
func isFieldKey(key string, rows []row.Row) bool {
	if key == sortCreatedAt || key == sortUpdatedAt {
		return true
	}
	for _, r := range rows {
		if _, ok := r.Fields[key]; ok {
			return true
		}
	}
	return false
}

// sortByField orders the row indexes order by spec, stable (ties keep the
// list's own order); rows without a value come last in both directions.
func sortByField(order []int, rows []row.Row, spec sortspec.Spec) {
	specs := []sortspec.Spec{spec}
	slices.SortStableFunc(order, func(a, b int) int {
		return sortspec.Compare(specs,
			func(k string) sortspec.Value { return fieldValue(rows[a], k) },
			func(k string) sortspec.Value { return fieldValue(rows[b], k) })
	})
}

// fieldValue is a row's sortable value of key: the envelope times, else
// the field by its kind (a number numerically, a date or a text that is an
// RFC 3339 time by instant, a list by its first item, anything else as
// text).
func fieldValue(r row.Row, key string) sortspec.Value {
	switch key {
	case sortCreatedAt:
		return sortspec.Time(r.CreatedAt)
	case sortUpdatedAt:
		return sortspec.Time(r.UpdatedAt)
	}
	return sortValue(r.Fields[key])
}

// sortValue is the sortable form of one field value (nil = Missing).
func sortValue(v value.Value) sortspec.Value {
	switch t := v.(type) {
	case nil:
		return sortspec.Missing
	case value.Number:
		n, err := strconv.ParseFloat(t.String(), 64)
		if err != nil {
			return sortspec.Text(t.String())
		}
		return sortspec.Number(n)
	case value.Date:
		return sortspec.Time(t.Time())
	case value.DateTime:
		return sortspec.Time(t.Time())
	case value.List:
		items := t.Items()
		if len(items) == 0 {
			return sortspec.Missing
		}
		return sortValue(items[0])
	}
	s := v.String()
	if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return sortspec.Time(at)
	}
	return sortspec.Text(s)
}
