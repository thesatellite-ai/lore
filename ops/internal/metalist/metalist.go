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
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/trash"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/metafilter"
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
	Sort  string   `json:"sort,omitempty" flag:"sort" desc:"sort by a metadata key: meta.<key>, meta.<key>:asc or meta.<key>:desc (rows without it last)"`
	// Archived keeps archived rows in --meta filters (METADATA 3.19:
	// archived rows count as in use but are hidden from filters unless
	// --archived; memory and rule lists also show them only with it).
	Archived bool `json:"archived,omitempty" flag:"archived" desc:"include archived rows (also in --meta filters)"`
	Trash
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
	return []string{"in the trash: " + strings.Join(in, ", ") + " (lore trash show <id>; lore trash restore <id>)"}
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
	fArchivedAt                   = "archived_at" // every archivable table's column
)

// Keys reads the visible key definitions of every attached store.
func Keys(ctx context.Context, env Lister) ([]metafilter.Key, error) {
	var out []metafilter.Key
	for _, src := range env.Stores() {
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
			d := metafilter.Key{ID: id, Name: text(k, fName), Scope: text(k, fScope), Type: t, Retired: text(k, fMerged) != ""}
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

// Select filters and sorts rows by f. With no flag it returns every index
// in input order and reads nothing.
func Select(ctx context.Context, env Lister, f Flags, rows []row.Row) (Result, error) {
	all := make([]int, len(rows))
	for i := range rows {
		all[i] = i
	}
	if f.Empty() {
		return Result{Order: all}, nil
	}
	keys, err := Keys(ctx, env)
	if err != nil {
		return Result{}, err
	}
	p, warnings, err := metafilter.Compile(f.Meta, f.Place, f.Sort, keys)
	if err != nil {
		return Result{}, err
	}
	res := Result{Notes: warnings, Columns: p.Columns}
	var skipped []string
	var unchecked []string
	for _, i := range all {
		r := rows[i]
		if len(f.Meta) > 0 && !f.Archived && text(r, fArchivedAt) != "" {
			continue // 3.19: archived rows are hidden from --meta filters
		}
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
	res.Notes = append(res.Notes, skipped...)
	for _, id := range unchecked {
		res.Notes = append(res.Notes, "note: 1 row not checked: "+id+" has quarantined metadata")
	}
	return res, nil
}
