package engine

// Purging a row takes the rows that only say something about it
// (INTEG-TRASH-6, owner 2026-11-07): its comments, its commit links and
// every knowledge link to or from it. Which rows those are is declared
// once in the schema: a Polymorphic relation with PurgeWithTarget
// (comments and commit_links' entity relation, knowledge_refs' src and
// dst). Those relations are tombstone_only (INTEG-TRASH-1): a trash move
// leaves the rows live so a restore finds them intact. A purge is final,
// so a row left behind would be an orphan nobody can reach from its
// target: every such row (live or itself in the trash) is purged in the
// same remove plan, exactly as `lore purge <id>` would purge it (a
// tombstone plus the id in _purged.json), so sync and E_PURGED treat it
// like any purged id.
//
//	remove(purge) ─► planDelete(purge) ─► purgedWith(row) ─► cascade steps
//	PurgeOlder(dry) / PurgePlan ─► purgedWithAll(rows) ─► TrashResult.Taken

import (
	"bytes"
	"context"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// purgeRules are the PurgeWithTarget relations that can point at target
// (empty when no registered table declares one).
func (e *Engine) purgeRules(target row.Table) []dependentRule {
	var out []dependentRule
	for _, r := range e.dependentRules(target) {
		if r.purgeWith && r.typeField != "" && !r.list {
			out = append(out, r)
		}
	}
	return out
}

// purgeWithTables is the set of tables holding a PurgeWithTarget
// relation: a remove's cascade rows of these tables are what a purge
// took with its rows (purgedTaken).
func (e *Engine) purgeWithTables() map[row.Table]bool {
	out := map[row.Table]bool{}
	for _, def := range e.reg.Tables() {
		for _, rel := range def.Relations {
			if rel.PurgeWithTarget {
				out[def.Name] = true
			}
		}
	}
	return out
}

// purgedWith returns every row a purge of (table, id) takes with it, live
// or in the trash, in each writable store that records purged ids (a
// store this person cannot write, or cannot purge in, is left as it is
// rather than refusing the whole purge). An id already listed as purged,
// or a tombstone, is skipped, so a second purge finds nothing (run twice
// = once). One place attached twice is read once.
func (e *Engine) purgedWith(ctx context.Context, table row.Table, id ids.AnyID) ([]dependent, error) {
	rules := e.purgeRules(table)
	if len(rules) == 0 {
		return nil, nil
	}
	tables := map[row.Table]bool{}
	for _, r := range rules {
		tables[r.table] = true
	}
	var out []dependent
	seen := map[string]bool{}
	for _, name := range e.allStores() {
		st, err := e.writable(name)
		if err != nil {
			continue // read-only: nothing here can be purged
		}
		if _, ok := st.Backend.(Purger); !ok {
			continue
		}
		if st.Location != "" {
			if seen[st.Location] {
				continue
			}
			seen[st.Location] = true
		}
		set, err := purgedSet(ctx, st)
		if err != nil {
			return nil, err
		}
		rows, _, err := readAll(ctx, st)
		if err != nil {
			return nil, err
		}
		for _, sr := range rows {
			if !tables[sr.Table] || sr.Deleted || set[sr.ID.String()] || !bytes.Contains(sr.Doc, []byte(id.String())) {
				continue
			}
			r, derr := sr.Decode()
			if derr != nil {
				return nil, derr
			}
			for _, rule := range rules {
				if rule.table == r.Table && points(r, rule, table, id) {
					out = append(out, dependent{source: name, row: r, rule: rule})
					break // a self link (src and dst both the row) is taken once
				}
			}
		}
	}
	return out, nil
}

// purgedWithAll lists the rows a purge of rows would take with them
// (recursively: a comment on one of those links too), for a dry run.
func (e *Engine) purgedWithAll(ctx context.Context, rows []Trashed) ([]Changed, error) {
	// One read of each store for the whole list: purgedWith read every
	// store again per row (bug hunt G-8: a dry run over 65 trashed rows
	// took twice the delete that trashed them). It writes nothing, so the
	// snapshot never goes stale here.
	ctx = EnsureReadSnapshot(ctx)
	var out []Changed
	seen := map[ids.AnyID]bool{}
	type item struct {
		table row.Table
		id    ids.AnyID
	}
	var queue []item
	for _, t := range rows {
		seen[t.Row.ID] = true
		queue = append(queue, item{t.Row.Table, t.Row.ID})
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		cs, err := e.purgedWith(ctx, it.table, it.id)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if seen[c.row.ID] {
				continue
			}
			seen[c.row.ID] = true
			out = append(out, Changed{Store: c.source, Row: c.row})
			queue = append(queue, item{c.row.Table, c.row.ID})
		}
	}
	return out, nil
}

// PurgePlan is what `lore trash purge <id|bat_> --dry-run` lists: the
// trashed rows TrashTarget resolves plus the comments and links a purge
// would take with them (INTEG-TRASH-6). It writes nothing.
func (e *Engine) PurgePlan(ctx context.Context, target string) (TrashResult, error) {
	res, err := e.TrashTarget(ctx, target)
	if err != nil {
		return TrashResult{}, err
	}
	res.Taken, err = e.purgedWithAll(ctx, res.Rows)
	return res, err
}

// purgedTaken is the rows of a remove's Written that a purge took with
// it (its cascade rows of a PurgeWithTarget table), for the purge result.
func (e *Engine) purgedTaken(w Written) []Changed {
	tables := e.purgeWithTables()
	var out []Changed
	for _, c := range w.Also {
		if tables[c.Row.Table] {
			out = append(out, c)
		}
	}
	return out
}
