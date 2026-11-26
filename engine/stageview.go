package engine

import (
	"context"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// Staged view (devco:D-931, H-192). A dry run that runs its real write
// path check-only writes nothing, so a later step that reads what an
// earlier step wrote (meta edit --type converts every value after it
// retypes the key; meta rehome sets values under the key rows it adds)
// read the old rows and failed, or passed, where the write would not. Such
// an operation runs its dry run in a stage (Staging + WithCheckOnly, then
// CommitEach: every role and --store check, nothing written), and the
// reads that check later writes see the stage's rows on top of the store:
//
//	metaRegistry: List(meta_keys, meta_values) ─► withStaged ─► key types,
//	strict values, scopes as the stage leaves them

// Staged reads (devco:D-1002, store growth item 4). A real stage's rows
// were never read back, so an operation whose later row reads an earlier
// one (task import: a sub-task reads its parent and its siblings' ranks)
// committed its batch so far before each such row: one store commit per
// task-tree record. WithStagedReads makes a real stage readable as a
// check-only one is: Get (getRow, staleNow) sees the stage's rows
// (stagedView) and List lays them over the store's (withStaged).

// stagedReadsKey is the context key of WithStagedReads.
type stagedReadsKey struct{}

// WithStagedReads is ctx whose reads see the rows its open stage holds
// (Get and List of the stage's store), so an operation can stage rows that
// read each other back and commit them as one batch. Outside a stage it
// changes nothing.
func WithStagedReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, stagedReadsKey{}, true)
}

// stagedReads reports whether ctx's reads see its open stage: a
// check-only run's always do (D-932), a real stage's when the caller asked
// (WithStagedReads).
func stagedReads(ctx context.Context) bool { return stagedReadsAsked(ctx) || CheckOnly(ctx) }

// stagedReadsAsked reports whether ctx carries WithStagedReads: only then
// does List lay the stage's rows over the store's (a check-only run's List
// stays as it was, D-932).
func stagedReadsAsked(ctx context.Context) bool {
	on, _ := ctx.Value(stagedReadsKey{}).(bool)
	return on
}

// withStaged is rows (one table of store name) as ctx's open stage leaves
// them: a row the stage changes is replaced by its last staged version
// (dropped when that is a tombstone, unless the caller lists deleted
// rows), a row the stage creates is appended. Without a stage, or when
// the stage holds nothing of that table there: rows unchanged.
func withStaged(ctx context.Context, name store.Source, table row.Table, rows []row.Row) []row.Row {
	s := stageOf(ctx)
	if s == nil {
		return rows
	}
	s.mu.Lock()
	var last []store.Row
	if p, ok := s.bySrc[name]; ok {
		seen := map[store.Key]int{}
		for _, c := range p.changes {
			if c.Row.Table != table {
				continue
			}
			if i, dup := seen[c.Row.Key()]; dup {
				last[i] = c.Row
				continue
			}
			seen[c.Row.Key()] = len(last)
			last = append(last, c.Row)
		}
	}
	s.mu.Unlock()
	if len(last) == 0 {
		return rows
	}
	out := make([]row.Row, 0, len(rows)+len(last))
	byID := map[string]int{}
	for _, r := range rows {
		byID[r.ID.String()] = len(out)
		out = append(out, r)
	}
	var drop []int
	for _, sr := range last {
		r, err := row.Decode(sr.Doc)
		if err != nil {
			continue // a staged row always decodes (put encoded it); skip rather than guess
		}
		i, had := byID[r.ID.String()]
		switch {
		case had && r.Deleted:
			drop = append(drop, i)
		case had:
			out[i] = r
		case !r.Deleted:
			out = append(out, r)
		}
	}
	if len(drop) == 0 {
		return out
	}
	gone := map[int]bool{}
	for _, i := range drop {
		gone[i] = true
	}
	kept := out[:0]
	for i, r := range out {
		if !gone[i] {
			kept = append(kept, r)
		}
	}
	return kept
}
