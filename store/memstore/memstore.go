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

// clone copies the document bytes so no caller can change stored data.
func clone(r store.Row) store.Row {
	r.Doc = append([]byte(nil), r.Doc...)
	return r
}
