// Package memstore is the REFERENCE and TEST implementation of
// store.Backend: rows in memory, nothing persisted. It exists to prove the
// contract suite (store/storetest) and to give engine and surface tests a
// backend with no files and no git. It is not a lore store type a person
// can attach.
package memstore

import (
	"context"
	"net/url"
	"strconv"
	"sync"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// Scheme is the url scheme the reference Opener answers ("mem://name").
const Scheme = "mem"

// Data is one store's rows: the "location" several Backends may open at
// once (a read-write and a read-only handle, as two processes would).
type Data struct {
	mu   sync.Mutex
	rows map[store.Key]entry
	seq  uint64
	// journal is the pending audit records (store.AuditJournal), oldest
	// first; it lives with the rows, so every handle sees the same set.
	journal []store.AuditRecord
}

// entry is a stored row and the sequence number of the batch that wrote it.
type entry struct {
	row store.Row
	seq uint64
}

// NewData returns an empty location.
func NewData() *Data { return &Data{rows: map[store.Key]entry{}} }

// Open returns a Backend over d.
func (d *Data) Open(opts store.OpenOptions) *Backend {
	return &Backend{data: d, readOnly: opts.ReadOnly}
}

// Opener returns a store.Opener that opens d for every mem:// url.
func Opener(d *Data) store.Opener {
	return func(_ context.Context, _ *url.URL, opts store.OpenOptions) (store.Backend, error) {
		return d.Open(opts), nil
	}
}

// Backend is one open handle on a Data.
type Backend struct {
	data     *Data
	readOnly bool

	mu     sync.Mutex
	closed bool
}

var _ store.Backend = (*Backend)(nil)

// usable fails after Close or when ctx is done.
func (b *Backend) usable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errs.Wrap(errs.Internal, "memstore: closed", store.ErrClosed)
	}
	return nil
}

// Read implements store.Backend. The cursor is the decimal sequence number
// of the last batch seen; anything unreadable means "from the beginning".
func (b *Backend) Read(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	if err := b.usable(ctx); err != nil {
		return nil, "", err
	}
	after, err := strconv.ParseUint(string(cursor), 10, 64)
	if err != nil {
		after = 0
	}
	d := b.data
	d.mu.Lock()
	defer d.mu.Unlock()
	if after > d.seq { // a cursor from another location: start over
		after = 0
	}
	var out []store.Row
	for _, e := range d.rows {
		if e.seq > after {
			out = append(out, clone(e.row))
		}
	}
	return out, store.Cursor(strconv.FormatUint(d.seq, 10)), nil
}

// Get implements store.Backend.
func (b *Backend) Get(ctx context.Context, table row.Table, id ids.AnyID) (store.Row, error) {
	if err := b.usable(ctx); err != nil {
		return store.Row{}, err
	}
	d := b.data
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.rows[store.Key{Table: table, ID: id}]
	if !ok {
		return store.Row{}, store.ErrNotFound
	}
	return clone(e.row), nil
}

// Write implements store.Backend: every Expect is checked under one lock
// before anything is applied, so a batch is all or none.
func (b *Backend) Write(ctx context.Context, changes []store.Change) error {
	return b.write(ctx, changes, nil)
}

// Journaled is a Backend that also implements store.AuditJournal (the
// pending journal lives in the Data, beside the rows). It is a separate
// type, not a method set on Backend, because the capability is optional
// and the engine changes path on it: engine tests that pin the plain
// Write path keep the plain Backend, and the contract suite runs both
// (later 543).
type Journaled struct{ *Backend }

var _ store.AuditJournal = Journaled{}

// OpenJournaled returns a journal-capable Backend over d.
func (d *Data) OpenJournaled(opts store.OpenOptions) Journaled {
	return Journaled{d.Open(opts)}
}

// WriteAudited implements store.AuditJournal: the rows and recs land under
// the same lock, or neither does (a refused batch keeps no record).
func (b Journaled) WriteAudited(ctx context.Context, changes []store.Change, recs []store.AuditRecord) error {
	return b.write(ctx, changes, recs)
}

// PendingAudit implements store.AuditJournal: the records not yet
// acknowledged, oldest first (copies; a caller cannot change the journal).
func (b Journaled) PendingAudit(ctx context.Context) ([]store.AuditRecord, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	d := b.data
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]store.AuditRecord, 0, len(d.journal))
	for _, r := range d.journal {
		out = append(out, cloneRec(r))
	}
	return out, nil
}

// AckAudit implements store.AuditJournal: drops the pending records whose
// Key and HLC match one of acked; an unknown record is ignored.
func (b Journaled) AckAudit(ctx context.Context, acked []store.AuditRecord) error {
	if err := b.usable(ctx); err != nil {
		return err
	}
	d := b.data
	d.mu.Lock()
	defer d.mu.Unlock()
	keep := d.journal[:0]
	for _, r := range d.journal {
		drop := false
		for _, a := range acked {
			if a.Key == r.Key && a.HLC.Compare(r.HLC) == 0 {
				drop = true
				break
			}
		}
		if !drop {
			keep = append(keep, r)
		}
	}
	d.journal = keep
	return nil
}

// write is Write with recs appended to the journal in the same batch.
func (b *Backend) write(ctx context.Context, changes []store.Change, recs []store.AuditRecord) error {
	if err := b.usable(ctx); err != nil {
		return err
	}
	if b.readOnly {
		return store.ErrReadOnly
	}
	seen := make(map[store.Key]struct{}, len(changes))
	for _, c := range changes {
		r := c.Row
		if !r.Table.Valid() || r.ID.IsZero() || r.HLC.IsZero() || len(r.Doc) == 0 {
			return errs.New(errs.InvalidInput, "memstore: change without table, id, hlc or document")
		}
		if _, dup := seen[r.Key()]; dup {
			return errs.Wrap(errs.InvalidInput, "memstore: batch", store.ErrBatch)
		}
		seen[r.Key()] = struct{}{}
	}
	d := b.data
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range changes {
		cur, exists := d.rows[c.Row.Key()]
		switch {
		case c.Expect.IsZero() && exists:
			return store.ErrStale
		case !c.Expect.IsZero() && (!exists || cur.row.HLC.Compare(c.Expect) != 0):
			return store.ErrStale
		}
	}
	d.seq++
	for _, c := range changes {
		d.rows[c.Row.Key()] = entry{row: clone(c.Row), seq: d.seq}
	}
	for _, r := range recs {
		d.journal = append(d.journal, cloneRec(r))
	}
	return nil
}

// Sync implements store.Backend: there is no remote.
func (b *Backend) Sync(ctx context.Context) (store.SyncReport, error) {
	if err := b.usable(ctx); err != nil {
		return store.SyncReport{}, err
	}
	return store.SyncReport{}, nil
}

// Capabilities implements store.Backend: none.
func (b *Backend) Capabilities() store.Capabilities { return store.Capabilities{} }

// Close implements store.Backend; idempotent.
func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

// cloneRec copies a record's line so no caller can change the journal.
func cloneRec(r store.AuditRecord) store.AuditRecord {
	r.Line = append([]byte(nil), r.Line...)
	return r
}

// clone copies the document bytes so no caller can change stored data.
func clone(r store.Row) store.Row {
	r.Doc = append([]byte(nil), r.Doc...)
	return r
}
