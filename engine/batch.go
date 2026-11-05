package engine

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// Batch is one bulk action (XQ1): every row it writes carries its one
// bat_ id and its kind on the audit event, so `lore batch list|show` read
// batches from the batch log; a batch is never a row.
type Batch struct {
	ID   ids.ID[ids.Batch]
	Kind string
}

// IsZero reports "no batch".
func (b Batch) IsZero() bool { return b.ID.IsZero() }

// batchKey is the context key of the current batch.
type batchKey struct{}

// BatchTrailer is the commit trailer key naming the bulk action a store
// commit belongs to (XQ1, r3-batch): "Lore-Batch: bat_… <kind>".
const BatchTrailer = "Lore-Batch"

// WithBatch marks every write made with ctx as part of b (XQ1).
func WithBatch(ctx context.Context, b Batch) context.Context {
	return context.WithValue(ctx, batchKey{}, b)
}

// withBatchTrailer attaches the Lore-Batch commit trailer of ctx's batch
// (XQ1, r3-batch), so a backend that commits (a git store clone) names the
// bulk action in its commit; a backend without commits ignores it. It is
// skipped when ctx already carries a trailer naming the same bat_ id (a
// soft delete's Lore-Delete-Batch), so a commit never names it twice.
func withBatchTrailer(ctx context.Context) context.Context {
	b, ok := BatchOf(ctx)
	if !ok {
		return ctx
	}
	for _, t := range store.CommitTrailers(ctx) {
		if strings.Contains(t, b.ID.String()) {
			return ctx
		}
	}
	line := BatchTrailer + ": " + b.ID.String()
	if b.Kind != "" && !strings.ContainsAny(b.Kind, "\n\r") {
		line += " " + b.Kind
	}
	return store.WithCommitTrailers(ctx, line)
}

// BatchOf returns the batch ctx carries.
func BatchOf(ctx context.Context) (Batch, bool) {
	b, ok := ctx.Value(batchKey{}).(Batch)
	return b, ok && !b.IsZero()
}

// importKey is the context key of the current import's _o.import slot.
type importKey struct{}

// WithImport sets the _o.import slot of every row created with ctx
// (XQ9 / PROV9: an importer passes its source and the external id).
func WithImport(ctx context.Context, imp origin.Import) context.Context {
	return context.WithValue(ctx, importKey{}, imp)
}

// importOf returns the import slot ctx carries.
func importOf(ctx context.Context) (origin.Import, bool) {
	i, ok := ctx.Value(importKey{}).(origin.Import)
	return i, ok && !i.IsZero()
}

// Confirmed reports whether the running operation's caller already
// confirmed (--yes, "yes": true). XQ2: a bulk action decides only after
// counting its rows whether it must ask, so it cannot use the spec's
// up-front ConfirmRule; it answers E_NEEDS_CONFIRM itself above
// batch.preview_rows unless this is true (the CLI then prompts a terminal
// and re-runs with yes; REST passes the request's yes). Outside Run: false.
func Confirmed(ctx context.Context) bool {
	c, ok := ctx.Value(callKey{}).(call)
	return ok && c.caller.Yes
}

// Interactive reports whether the running operation's caller is a person
// at a terminal (op.Caller.Interactive): an op that reads a value from the
// terminal (resource --prompt, RES 3.3) refuses otherwise, so REST and
// pipes never wait. Outside Run: false.
func Interactive(ctx context.Context) bool {
	c, ok := ctx.Value(callKey{}).(call)
	return ok && c.caller.Interactive
}

// Staged writes (PC-RES-1, FB-RES-1): a multi-row operation that must land
// all or none (resource import) runs its ordinary Create / Update calls
// with a staging ctx. Every row is validated, scanned and policy-checked
// as usual, but put hands its backend changes to the stage instead of the
// backend, and finishW hands it the cache rows and audit events. Commit
// then writes every staged change in ONE Backend.Write (all or none by the
// store contract, the audit records in the same batch on an AuditJournal
// backend) and only then refreshes the cache and records the audit.
//
//	Staging(ctx) → Create/Update… (put: stage.addWrite; finishW: stage.addFinish)
//	Commit(ctx)  → one store? → writeChanges(all) → finishW(all rows, all events)
//	error / no Commit → nothing was written
//
// Staged rows are NOT visible to reads made with the staging ctx (List,
// Get read the backend): the caller resolves references between its own
// rows itself (the import pins ids). A stage that spans two stores is
// refused at Commit before any write, because no backend batch covers two
// stores.
type stage struct {
	mu      sync.Mutex
	e       *Engine
	closed  bool
	order   []store.Source
	bySrc   map[store.Source]*stagedStore
	written map[store.Key]bool
	created map[store.Key]bool // rows whose create event is staged
}

// stagedStore is what one store's part of a stage holds.
type stagedStore struct {
	st      Attached
	changes []store.Change
	journal []Event // put's events, for the backend's audit journal
	docs    []store.Row
	events  []Event // finishW's full events, recorded after the write
}

// stageKey is the context key of the current stage.
type stageKey struct{}

// Staging returns ctx with a new, empty stage: every engine write made
// with it is held until Commit (FB-RES-1). Nesting replaces the stage.
func Staging(ctx context.Context) context.Context {
	return context.WithValue(ctx, stageKey{}, &stage{bySrc: map[store.Source]*stagedStore{}, written: map[store.Key]bool{}, created: map[store.Key]bool{}})
}

// stageOf returns ctx's open stage, nil when there is none (or it was
// committed: Commit's own write must reach the backend).
func stageOf(ctx context.Context) *stage {
	s, ok := ctx.Value(stageKey{}).(*stage)
	if !ok || s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	return s
}

// part returns (making it) the staged part of store st.
func (s *stage) part(e *Engine, st Attached) *stagedStore {
	if s.e == nil {
		s.e = e
	}
	p, ok := s.bySrc[st.Name]
	if !ok {
		p = &stagedStore{st: st}
		s.bySrc[st.Name] = p
		s.order = append(s.order, st.Name)
	}
	return p
}

// addWrite holds one put's changes and journal events. A second create of
// a row the stage already creates (an actor row two staged writes both
// ensure) is dropped: the first one stands. Any other second write of a
// staged row is ErrBatch: the stage cannot read its own rows back, so it
// cannot check that write against the staged version.
func (s *stage) addWrite(e *Engine, st Attached, changes []store.Change, evs []Event) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	main := changes[0].Row.Key()
	if s.written[main] {
		if changes[0].Expect.IsZero() {
			return false, nil
		}
		return false, errs.Wrap(errs.InvalidInput, "engine: staged batch writes "+main.ID.String()+" twice", store.ErrBatch)
	}
	p := s.part(e, st)
	for _, c := range changes {
		k := c.Row.Key()
		if s.written[k] {
			return false, errs.Wrap(errs.InvalidInput, "engine: staged batch writes "+k.ID.String()+" twice", store.ErrBatch)
		}
		s.written[k] = true
	}
	p.changes = append(p.changes, changes...)
	p.journal = append(p.journal, evs...)
	return true, nil
}

// addFinish holds one write's cache rows and audit events. Rows of a
// dropped duplicate create (addWrite) are skipped, so the cache and the
// audit see each row once.
func (s *stage) addFinish(e *Engine, source store.Source, rows []store.Row, evs []Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := e.stores[source]
	if !ok {
		st = Attached{Name: source}
	}
	p := s.part(e, st)
	have := map[store.Key]bool{}
	for _, d := range p.docs {
		have[d.Key()] = true
	}
	for _, r := range rows {
		if !have[r.Key()] {
			p.docs = append(p.docs, r)
			have[r.Key()] = true
		}
	}
	for _, ev := range evs {
		k := store.Key{Table: ev.Table, ID: ev.ID}
		if ev.Action == ActionCreate && s.created[k] {
			continue // the dropped duplicate create's event
		}
		if ev.Action == ActionCreate {
			s.created[k] = true
		}
		p.events = append(p.events, ev)
	}
}

// Commit writes everything staged on ctx as one backend batch, then
// refreshes the cache and records the audit (FB-RES-1). Nothing staged:
// no-op. A stage naming two stores is E_INVALID_INPUT with nothing
// written. A failed write leaves the store as it was (Backend.Write is
// all or none). The stage is closed either way: a second Commit is a
// no-op, and a write made with ctx afterwards goes straight to the store.
func Commit(ctx context.Context) ([]Warning, error) {
	s, ok := ctx.Value(stageKey{}).(*stage)
	if !ok || s == nil {
		return nil, nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil
	}
	s.closed = true
	e, order := s.e, slices.Clone(s.order)
	s.mu.Unlock()
	if e == nil {
		return nil, nil
	}
	var touched []store.Source
	for _, name := range order {
		if len(s.bySrc[name].changes) > 0 {
			touched = append(touched, name)
		}
	}
	if len(touched) > 1 {
		names := make([]string, len(touched))
		for i, n := range touched {
			names[i] = string(n)
		}
		return nil, errs.New(errs.InvalidInput, "engine: one batch cannot write to two stores ("+strings.Join(names, ", ")+"); nothing written").
			WithHint("write each store's rows as its own batch")
	}
	if len(touched) == 0 {
		return nil, nil
	}
	p := s.bySrc[touched[0]]
	if err := e.writeChanges(ctx, p.st, p.changes, p.journal); err != nil {
		return nil, err
	}
	return e.finishW(ctx, p.st.Name, p.docs, p.events...)
}

// createIDKey is the context key of a pinned create id.
type createIDKey struct{}

// WithCreateID makes the next Create of a row of id's table made with ctx
// use id instead of a fresh one (FB-RES-1: an import pins each new row's
// id first, so refs between the rows of the file resolve before any of
// them is written). It is CreateWithID through the ordinary Create path,
// so every guard a surface wraps around Create still runs. Creates of
// other tables made with ctx (an actor row the write ensures) are not
// affected; the row must not exist yet (E_STALE), as with CreateWithID.
func WithCreateID(ctx context.Context, id ids.AnyID) context.Context {
	return context.WithValue(ctx, createIDKey{}, id)
}

// createIDOf is ctx's pinned create id when its prefix is p.
func createIDOf(ctx context.Context, p ids.Prefix) (ids.AnyID, bool) {
	id, ok := ctx.Value(createIDKey{}).(ids.AnyID)
	if !ok || id.IsZero() || id.Prefix() != p {
		return ids.AnyID{}, false
	}
	return id, true
}
