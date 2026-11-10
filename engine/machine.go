package engine

// machine.go — routing of machine-local tables (PASS3 phase C, P3-LOCAL-1).
//
// A table whose definition is MachineLocal lives only in the machine store
// (store.SourceMachine): one local SQLite file per machine, never synced.
// Operations need no special code for it: they call Create/Update/Delete/
// List/Get as for any table, and the engine sends the row to the machine
// store whatever target the call names, so the same validate → scan →
// policy → stamp → write → cache → audit path runs.
//
//	op ──Create(target, table)──▶ routeTable(target, def)
//	        def.MachineLocal ──▶ store.SourceMachine (refused when none attached)
//	        else, target is the machine store ──▶ E_INVALID_INPUT
//	        else ──▶ target unchanged
//	op ──Get/Update/Delete(ref)──▶ lookupStores(table): machine only / e.order
//	op ──List(source, table)──▶ machine-local table: the machine store
//
// The machine store is attached with WithMachineStore, outside e.order:
// Stores() never lists it, so store listings, `--to` defaults, sync loops
// and search order are unchanged by its presence.

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// WithMachineStore attaches this machine's machine-local store. a.Name must
// be store.SourceMachine; its Level defaults to policy.LevelMachine. It is
// optional: without it a machine-local table is E_NOT_IMPLEMENTED (an
// embedder or test that never needs one pays nothing).
func WithMachineStore(a Attached) Option {
	return func(e *Engine) error {
		if a.Name != store.SourceMachine || a.Backend == nil {
			return errs.New(errs.ConfigInvalid, "the machine store is named "+string(store.SourceMachine)+" and needs a backend")
		}
		if _, dup := e.stores[a.Name]; dup {
			return errs.New(errs.ConfigInvalid, "store "+string(a.Name)+" attached twice")
		}
		if a.Level == "" {
			a.Level = policy.LevelMachine
		}
		e.stores[a.Name] = a
		e.machine = true
		return nil
	}
}

// HasMachineStore reports whether the machine store is attached.
func (e *Engine) HasMachineStore() bool { return e.machine }

// IsMachineLocal reports whether table's rows live in the machine store.
func (e *Engine) IsMachineLocal(table row.Table) bool {
	def, ok := e.reg.Table(table)
	return ok && def.MachineLocal
}

// routeTable is the store a new row of def goes to when the call names
// target (P3-LOCAL-1): a machine-local table always goes to the machine
// store, and the machine store takes nothing else.
func (e *Engine) routeTable(target store.Source, def schema.TableDef) (store.Source, error) {
	if def.MachineLocal {
		if !e.machine {
			return "", errs.New(errs.NotImplemented, ""+string(def.Name)+" is machine-local and no machine store is attached")
		}
		return store.SourceMachine, nil
	}
	if target == store.SourceMachine {
		return "", errs.New(errs.InvalidInput, ""+string(def.Name)+" rows travel with a store; the machine store keeps only machine-local tables").
			WithHint("write to the repo or an attached store with --to")
	}
	return target, nil
}

// lookupStores are the stores a lookup of table searches, in precedence
// order: the machine store alone for a machine-local table, else the
// attached stores (never the machine store).
func (e *Engine) lookupStores(table row.Table) []store.Source {
	if e.IsMachineLocal(table) {
		if !e.machine {
			return nil
		}
		return []store.Source{store.SourceMachine}
	}
	return e.order
}

// allStores is e.order plus the machine store when attached: the stores a
// walk over every row (dependents, trash) must cover so a machine-local
// row is found by the same rules as any other.
func (e *Engine) allStores() []store.Source {
	if !e.machine {
		return e.order
	}
	return append(slices.Clone(e.order), store.SourceMachine)
}

// listSource is the store a List of table reads: the machine store for a
// machine-local table whatever source the caller named.
func (e *Engine) listSource(source store.Source, table row.Table) store.Source {
	if e.machine && e.IsMachineLocal(table) {
		return store.SourceMachine
	}
	return source
}

// refuseMachineMove is a move's machine-store rule (P3-LOCAL-1): a
// machine-local row never travels to a store, and the machine store takes
// no moved row. Both are E_INVALID_INPUT. A move into the machine store is
// refused before any lookup (the rule holds for every id). A machine-local
// id is looked up in the machine store first, so an id that names no row
// is E_NOT_FOUND and a trashed one E_IN_TRASH, as every lookup answers
// (G-CORE-2, the lookup parity matrices); only a live row gets the rule.
func (e *Engine) refuseMachineMove(ctx context.Context, ref Ref, to store.Source) error {
	if to == store.SourceMachine {
		return errs.New(errs.InvalidInput, "the machine store keeps only machine-local rows; nothing moves into it").
			WithHint("move to the repo or an attached store")
	}
	table := ref.Table
	if table == "" {
		if def, ok := e.reg.ByPrefix(ref.ID.Prefix()); ok {
			table = def.Name
		}
	}
	if !e.IsMachineLocal(table) {
		return nil
	}
	src, cur, err := e.locate(ctx, Ref{Table: table, ID: ref.ID})
	if err != nil {
		return err
	}
	if cur.Deleted {
		st, aerr := e.attached(src)
		if aerr != nil {
			return aerr
		}
		return deletedErr(ctx, st, cur, "is deleted")
	}
	return errs.New(errs.InvalidInput, ""+string(table)+" rows are machine-local: they stay on this machine and never move to a store")
}
