package lint

// copiedid.go — copied_id: one row live in two stores (devco:D-929, bug
// hunt H-187, a leftover of H-178 / #142). `lore store adopt` copies a
// repo's rows into a store folder keeping their ids, and so does a copied
// row file. An edit then reaches one copy and the other keeps the old
// text; every list read shows only the newest copy (engine staleCopyOf),
// so the old one is out of sight, yet `lore show --id` with both stores
// attached answers E_AMBIGUOUS_ID and a later write can land on either.
//
//	copiedIDs(all)
//	  ├─ group live rows by (table, id), copies of one row only
//	  │   (engine.SameRowCopies: same created_at; never actors)
//	  ├─ the kept copy: newest HLC (equal: the nearest store's), the one
//	  │   list, search and ask show
//	  └─ one finding per other copy, fix = the bulk move of that one row
//	     into the kept copy's store, which leaves a tombstone where the
//	     stale copy was and keeps the kept copy's text
//
// Reported, never settled on its own: the kept copy is lore's pick, the
// person may want the other text.

import (
	"context"
	"hash/maphash"
	"slices"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// CopiedID is the finding of a row live in two stores.
const CopiedID Code = "copied_id"

// FindCopiedIDs is the copied_id findings of every attached store (lore
// doctor counts them and points at lore lint --code copied_id). When
// every store's row files can be named (rowNamer) only the rows whose id
// two stores hold are read (copyCandidates), else every row.
func FindCopiedIDs(ctx context.Context, env Env) ([]Finding, error) {
	if n, ok := env.(rowNamer); ok {
		all, named, err := copyCandidates(ctx, env, n)
		if err != nil {
			return nil, err
		}
		if named {
			return copiedIDs(all), nil
		}
	}
	all, err := ops{env: env}.rows(ctx)
	if err != nil {
		return nil, err
	}
	return copiedIDs(all), nil
}

// rowNamer is an Env that names each store's row files without reading
// them and reads rows by id (the wire App).
type rowNamer interface {
	RowNames(ctx context.Context, source store.Source) ([]store.RowName, bool, error)
	ListIDs(ctx context.Context, source store.Source, table row.Table, want []ids.AnyID) ([]row.Row, error)
}

// copyCandidates is the live rows, in store order as rows lists them, of
// every id of a copy-hiding table that two or more stores name (the only
// rows copiedIDs can report); named is false when a store's rows cannot
// be named, and the caller reads every row. Each store's names are seen
// once and dropped; only a 64-bit hash of each (table, id) is kept (a
// hash shared by two ids only reads a few rows more). Why (bug hunt
// H-276, devco:D-1014): copied_id read every row of every folder of a
// store attached whole, 67 s and 2 GB of lore doctor at 100k commits.
func copyCandidates(ctx context.Context, env Env, n rowNamer) (map[row.Table][]sourced, bool, error) {
	ctx = engine.WithEveryCopy(engine.WithRawRead(ctx)) // as rowsOf: both copies, the stored text
	hides := map[row.Table]bool{}
	for _, def := range schema.Builtin() {
		hides[def.Name] = engine.CopiesHide(def)
	}
	srcs := engine.ReadStoresOf(env)
	seed := maphash.MakeSeed()
	first := map[uint64]int{}                            // (table, id) hash ─▶ the first store naming it
	want := make([]map[row.Table][]ids.AnyID, len(srcs)) // per store: the ids to read
	add := func(i int, name store.RowName) {
		if want[i] == nil {
			want[i] = map[row.Table][]ids.AnyID{}
		}
		want[i][name.Table] = append(want[i][name.Table], name.ID) // ListIDs reads an id named twice once
	}
	listed := make([]map[row.Table][]row.Row, len(srcs)) // the stores that cannot name their rows: listed whole
	for i, src := range srcs {
		names, ok, err := n.RowNames(ctx, src)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			if listed[i], names, err = listHiding(ctx, env, src, hides); err != nil {
				return nil, false, err
			}
		}
		for _, name := range names {
			if !hides[name.Table] {
				continue // a machine-local table or actors: copiedIDs skips it
			}
			h := maphash.String(seed, string(name.Table)+"/"+name.ID.String())
			j, seen := first[h]
			if !seen {
				first[h] = i
				continue
			}
			if j != i {
				add(j, name)
				add(i, name)
			}
		}
	}
	out := map[row.Table][]sourced{}
	for i, src := range srcs {
		if listed[i] != nil {
			for t, rs := range listed[i] {
				for _, r := range rs {
					out[t] = append(out[t], sourced{src: src, r: r}) // ids no other store holds group with nothing
				}
			}
			continue
		}
		tables := make([]row.Table, 0, len(want[i]))
		for t := range want[i] {
			tables = append(tables, t)
		}
		slices.Sort(tables)
		for _, t := range tables {
			rs, err := n.ListIDs(ctx, src, t, want[i][t])
			if err != nil {
				return nil, false, err
			}
			for _, r := range rs {
				if !r.Deleted {
					out[t] = append(out[t], sourced{src: src, r: r})
				}
			}
		}
	}
	return out, true, nil
}

// listHiding is the live rows of every copy-hiding table of src (List)
// and their names: a store whose row files cannot be named (a code
// repo's folder) is read whole, as before.
func listHiding(ctx context.Context, env Env, src store.Source, hides map[row.Table]bool) (map[row.Table][]row.Row, []store.RowName, error) {
	rows := map[row.Table][]row.Row{}
	var names []store.RowName
	for _, def := range schema.Builtin() {
		if !hides[def.Name] {
			continue
		}
		rs, err := env.List(ctx, src, def.Name)
		if err != nil {
			return nil, nil, err
		}
		for _, r := range rs {
			if r.Deleted {
				continue
			}
			rows[def.Name] = append(rows[def.Name], r)
			names = append(names, store.RowName{Table: def.Name, ID: r.ID})
		}
	}
	return rows, names, nil
}

// copiedIDs reports every stale copy of a row another store holds too.
func copiedIDs(all map[row.Table][]sourced) []Finding {
	var out []Finding
	tables := make([]row.Table, 0, len(all))
	for t := range all {
		tables = append(tables, t)
	}
	slices.Sort(tables)
	hides := map[row.Table]bool{}
	for _, def := range schema.Builtin() {
		hides[def.Name] = engine.CopiesHide(def)
	}
	for _, t := range tables {
		if !hides[t] {
			continue // a machine-local table (read once per store name) or actors
		}
		for _, group := range copyGroups(all[t]) {
			kept := keptCopy(group)
			for _, s := range group {
				if s.src == kept.src {
					continue
				}
				id := s.r.ID.String()
				out = append(out, Finding{Code: CopiedID, ID: id, Store: string(s.src),
					Message: id + " is live in " + string(kept.src) + " and " + string(s.src) + ": two copies of one row (lore store adopt and a copied row file keep ids); list, search and ask show the newest copy, in " + string(kept.src) + ", and an edit reaches one copy only; compare them with lore show --id " + id + " --store " + string(kept.src) + " and lore show --id " + id + " --store " + string(s.src),
					Fix:     "keep the copy in " + string(kept.src) + ": lore row move --from " + string(s.src) + " --where id=" + id + " --to " + string(kept.src) + " --yes (a tombstone replaces the copy in " + string(s.src) + "; lore show --id " + id + " --store " + string(s.src) + " shows its text first)"})
			}
		}
	}
	return out
}

// copyGroups is each set of two or more copies of one row in rows (one
// table's live rows of every store, in store order).
func copyGroups(rows []sourced) [][]sourced {
	byID := map[string][]sourced{}
	var order []string
	for _, s := range rows {
		k := s.r.ID.String()
		if _, ok := byID[k]; !ok {
			order = append(order, k)
		}
		byID[k] = append(byID[k], s)
	}
	var out [][]sourced
	for _, k := range order {
		g := byID[k]
		if len(g) < 2 {
			continue
		}
		var copies []sourced
		for _, s := range g {
			if s.src != g[0].src && engine.SameRowCopies(g[0].r, s.r) {
				copies = append(copies, s)
			}
		}
		if len(copies) > 0 {
			out = append(out, append([]sourced{g[0]}, copies...))
		}
	}
	return out
}

// keptCopy is the copy every list read shows: the newest HLC, the first
// (nearest store) of equal ones.
func keptCopy(group []sourced) sourced {
	best := group[0]
	for _, s := range group[1:] {
		if s.r.HLC.Compare(best.r.HLC) > 0 {
			best = s
		}
	}
	return best
}

// oneCopyEach is rows with every stale copy (copiedIDs) left out: the
// checks that compare rows with each other (near_duplicate) see one row
// once, as list and search do.
func oneCopyEach(rows []sourced) []sourced {
	stale := map[store.Source]map[string]bool{}
	for _, g := range copyGroups(rows) {
		kept := keptCopy(g)
		for _, s := range g {
			if s.src != kept.src {
				if stale[s.src] == nil {
					stale[s.src] = map[string]bool{}
				}
				stale[s.src][s.r.ID.String()] = true
			}
		}
	}
	return slices.DeleteFunc(slices.Clone(rows), func(s sourced) bool { return stale[s.src][s.r.ID.String()] })
}
