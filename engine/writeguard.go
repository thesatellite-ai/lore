package engine

// writeguard.go — the one role check every row write passes (devco D-694,
// D-697, owner use pass 5): writeChanges and writePurge are the two sinks
// every write reaches (create, edit, merge, delete, trash, move, purge, a
// staged batch at Commit, import, bulk --where, batch undo, pin, policy,
// branch share/isolate), so a guard here sees every change whatever
// command made it. Pass 5 found two commands the per-command checks
// missed (a member's `rule edit --body` of a company must rule and a
// member's `branch share`); a check in the sink cannot miss one.
//
//	writeChanges / writePurge ──▶ guardWrites(changes)
//	     each change of a guarded table: before (the stored row, nil for a
//	     new one) and after (the row written, Deleted for a tombstone)
//	     ──▶ WriteGuard.GuardWrite(store, writes) ──▶ nil: write
//	                                                └─▶ error: nothing written

import (
	"context"
	"errors"
	"slices"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// GuardedWrite is one change the WriteGuard judges: Before is the row as
// stored before the write (nil for a new row), After the row written (its
// Deleted set for a delete or the source side of a move).
type GuardedWrite struct {
	Before *row.Row
	After  row.Row
}

// WriteGuard judges the changes of one write to one store before any of
// them lands (ops/profilestore.WriteGuard, installed by wire): an error
// refuses the whole write. Only rows of GuardedTables reach it.
type WriteGuard interface {
	GuardWrite(ctx context.Context, target store.Source, writes []GuardedWrite) error
}

// guardedTables are the tables whose rows the role rule covers: the
// knowledge every repo reading a folder is told (rules, hotfixes,
// memories, decisions), the folder's policies, and the org's teams (a
// member on @web adds rules in acme/web, so who is on a team is an
// admin's change, devco D-694 rule gap). Other rows (actors, projects,
// tasks, signals, the rest of the roster) have their own checks.
var guardedTables = []row.Table{schema.TableRules, schema.TableHotfixes, schema.TableMemories, schema.TableDecisions, schema.TablePolicies, schema.TableTeams}

// GuardedTables is a copy of the tables a WriteGuard is asked about.
func GuardedTables() []row.Table { return slices.Clone(guardedTables) }

// SetWriteGuard installs the role check of every write. Like
// SetRedactHook it is set after New, since the guard needs the object
// that owns the engine (wire.App). nil turns it off.
func (e *Engine) SetWriteGuard(g WriteGuard) { e.guard = g }

// guardKey marks a context already inside the guard, so a read the guard
// makes never asks it again.
type guardKey struct{}

// guardWrites asks the WriteGuard about changes to st: lore's own upkeep
// (WithSupportingRefresh) and tables outside guardedTables pass.
func (e *Engine) guardWrites(ctx context.Context, st Attached, changes []store.Change) error {
	if ctx.Value(guardKey{}) != nil {
		return nil
	}
	// D-869: agents.no_write of st, for every table (a task or an actor
	// row in a folder agents may not write is still a write there)
	if err := e.agentNoWriteChanges(ctx, st, changes); err != nil {
		return err
	}
	if e.guard == nil {
		return nil
	}
	var ws []GuardedWrite
	for _, c := range changes {
		if c.Upkeep || !slices.Contains(guardedTables, c.Row.Table) {
			continue
		}
		after, err := c.Row.Decode()
		if err != nil {
			return err
		}
		w := GuardedWrite{After: after}
		cur, err := st.Backend.Get(ctx, c.Row.Table, c.Row.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return err
		case !cur.Deleted:
			before, derr := cur.Decode()
			if derr != nil {
				return derr
			}
			w.Before = &before
		}
		ws = append(ws, w)
	}
	if len(ws) == 0 {
		return nil
	}
	return e.guard.GuardWrite(context.WithValue(ctx, guardKey{}, true), st.Name, ws)
}
