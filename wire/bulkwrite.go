package wire

// bulkwrite.go — one store commit for a multi-row write (owner report
// 2026-11-12: `lore actor merge` took 10 s a row, 66 rows 11.5 min, and
// `lore doctor --fix` 24 min for 85 links, because every row was its own
// write: a store commit, and on a push=now store a fetch and a push).
//
//	a.bulk(ctx, title)            one stage per store (engine.Staging)
//	  .update(ref, patch)         an ordinary Update made with the store's
//	                              stage: validated, scanned, audited as
//	                              usual, held until commit; a row that
//	                              cannot be written is recorded with its
//	                              reason and the rest go on
//	  .commit()                   each store's rows as ONE backend write:
//	                              one commit titled `lore: <title> (N
//	                              rows)` under one lock, one push
//	  .failed / .written          what was left (row, reason, fix) and the
//	                              rows written per store, for the output
//
// A rerun is the same command again: the rows already written no longer
// need the edit, so they are not planned again.

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// bulkFailure is one row a bulk write could not write.
type bulkFailure struct {
	Store  string
	Table  string
	ID     string
	Reason string
	Fix    string
}

// line is the failure as one output line.
func (f bulkFailure) line() string {
	s := f.Table + " " + f.ID + " in " + f.Store + ": " + f.Reason
	if f.Fix != "" {
		s += "; fix: " + f.Fix
	}
	return s
}

// bulkWrite is one multi-row write in progress.
type bulkWrite struct {
	a      *App
	ctx    context.Context
	stages map[store.Source]context.Context
	order  []store.Source
	// touched is every store a stage was opened for, in first-use order
	// (order is emptied by commit; this is kept for the output).
	touched []store.Source
	// pending counts the rows staged per store and not yet committed.
	pending map[store.Source]int
	// written counts the rows committed per store.
	written map[store.Source]int
	// commits is the commit each store's last write made (a store clone;
	// a store without commits has none), for the output (item 46: a step
	// that wrote says what it wrote).
	commits map[store.Source]string
	// staged lists the rows staged per store (to fail them all when the
	// store's commit fails).
	staged map[store.Source][]bulkFailure
	failed []bulkFailure
	// fixOf is the next step named for a row that failed without a hint.
	fixOf func(engine.Ref) string
}

// bulk starts a multi-row write whose store commits carry title.
func (a *App) bulk(ctx context.Context, title string, fixOf func(engine.Ref) string) *bulkWrite {
	return &bulkWrite{a: a, ctx: store.WithCommitTitle(ctx, title), stages: map[store.Source]context.Context{},
		pending: map[store.Source]int{}, written: map[store.Source]int{}, commits: map[store.Source]string{}, staged: map[store.Source][]bulkFailure{}, fixOf: fixOf}
}

// stage is src's open stage (made on first use).
func (b *bulkWrite) stage(src store.Source) context.Context {
	if s, ok := b.stages[src]; ok {
		return s
	}
	s := engine.Staging(b.ctx)
	b.stages[src] = s
	b.order = append(b.order, src)
	if !slices.Contains(b.touched, src) {
		b.touched = append(b.touched, src)
	}
	return s
}

// update stages one edit; false when the row could not be written (it is
// recorded in failed).
func (b *bulkWrite) update(ref engine.Ref, patch map[string]value.Value) bool {
	src := ref.Store
	if st, err := b.a.state(b.ctx); err == nil {
		src = b.target(st, ref.Table, src)
		if src == "" {
			src = refStore(b.ctx, st, ref)
		}
		ref.Store = src
	}
	_, err := b.a.Update(b.stage(src), ref, patch)
	if errors.Is(err, store.ErrBatch) {
		// the stage already writes this row (a second edit of it): land
		// what is staged, then write this edit in a fresh stage
		if cerr := b.commitStore(src); cerr == nil {
			_, err = b.a.Update(b.stage(src), ref, patch)
		}
	}
	f := bulkFailure{Store: string(src), Table: string(ref.Table), ID: ref.ID.String()}
	if err != nil {
		b.fail(f, err, ref)
		return false
	}
	b.pending[src]++
	b.staged[src] = append(b.staged[src], f)
	return true
}

// create stages a new row with a pinned id in src; false when it could
// not be written (recorded in failed).
func (b *bulkWrite) create(src store.Source, table row.Table, id ids.AnyID, fields map[string]value.Value) bool {
	if st, err := b.a.state(b.ctx); err == nil {
		src = b.target(st, table, src)
	}
	_, err := b.a.Create(engine.WithCreateID(b.stage(src), id), src, table, fields)
	f := bulkFailure{Store: string(src), Table: string(table), ID: id.String()}
	if err != nil {
		b.fail(f, err, engine.Ref{Store: src, Table: table, ID: id})
		return false
	}
	b.pending[src]++
	b.staged[src] = append(b.staged[src], f)
	return true
}

// target is the store a row of table named for src really lives in: the
// machine store for a machine-local table (runs, sessions, ...), whatever
// store the caller listed it from (engine routes such a row there, and
// List of any store reads it from there), else src. A bulk write keys its
// stages by this store, so a machine-local row lands in the machine
// store's own commit and never makes another store's stage span two
// stores (owner Mac run 2, item 33: `migrate actors` staged a repo's run
// rows with its tasks and wrote nothing: "one batch cannot write to two
// stores (repo, machine)").
func (b *bulkWrite) target(st *state, table row.Table, src store.Source) store.Source {
	if st.eng.HasMachineStore() && st.eng.IsMachineLocal(table) {
		return store.SourceMachine
	}
	return src
}

// fail records f with err's reason and next step.
func (b *bulkWrite) fail(f bulkFailure, err error, ref engine.Ref) {
	env := errs.EnvelopeOf(err)
	f.Reason = "[" + string(env.Error.Code) + "] " + env.Error.Message
	f.Fix = env.Error.Hint
	if f.Fix == "" && b.fixOf != nil {
		f.Fix = b.fixOf(ref)
	}
	b.failed = append(b.failed, f)
}

// commitStore lands src's stage as one backend write and closes it.
func (b *bulkWrite) commitStore(src store.Source) error {
	s, ok := b.stages[src]
	if !ok {
		return nil
	}
	delete(b.stages, src)
	_, err := engine.Commit(s)
	if err != nil {
		for _, f := range b.staged[src] {
			b.fail(f, errs.Prefix("the store write of "+string(src), err), engine.Ref{})
		}
	} else {
		b.written[src] += b.pending[src]
		if st, serr := b.a.state(b.ctx); serr == nil {
			if gb := gitOf(st.byName[src]); gb != nil && gb.CloneMode() { // repo mode writes files only: HEAD is not lore's
				b.commits[src] = gb.HeadCommit(b.ctx)
			}
		}
	}
	b.pending[src], b.staged[src] = 0, nil
	return err
}

// commit lands every store's stage (one write each, in the order first
// touched). The first commit error is returned; its rows are in failed.
func (b *bulkWrite) commit() error {
	var first error
	for _, src := range b.order {
		if err := b.commitStore(src); err != nil && first == nil {
			first = err
		}
	}
	b.order = nil
	return first
}

// commitText names the commits the write made, in the order the stores
// were first touched: "commit abc1234 in store shared" (joined with ", "
// for more than one store; "" when no store made a commit: a folder
// store, or nothing written).
func (b *bulkWrite) commitText() string {
	var parts []string
	for _, src := range b.touched {
		if c := b.commits[src]; c != "" {
			parts = append(parts, "commit "+shortCommit(c)+" in store "+string(src))
		}
	}
	return strings.Join(parts, ", ")
}

// total is the rows written in every store.
func (b *bulkWrite) total() int {
	n := 0
	for _, c := range b.written {
		n += c
	}
	return n
}

// failedErr is the error a bulk write ends with when rows were left: every
// row with its reason and fix, and that a rerun continues (exit non-zero).
// nil when every row was written.
func (b *bulkWrite) failedErr(verb, rerun string) error {
	if len(b.failed) == 0 {
		return nil
	}
	lines := make([]string, 0, len(b.failed))
	for _, f := range b.failed {
		lines = append(lines, "  "+f.line())
	}
	return errs.New(errs.Refused, verb+": wrote "+plural(b.total(), "row")+"; "+plural(len(b.failed), "row")+" could not be written:\n"+strings.Join(lines, "\n")).
		WithHint("fix each row as its line says, then run `" + rerun + "` again: it writes only what is left")
}
