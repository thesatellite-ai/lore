// Package store is the one seam between lore's core and the places rows are
// kept (STORE_SERVER_SPEC 5). A Backend stores and moves rows; it never
// decides merges, ids, scanning or policy: the core does that above it, the
// same for every backend, so swapping a backend can never change what lore
// allows.
//
// Layer L2: imports model only. Every backend must pass the contract suite
// in store/storetest; store/memstore is the reference implementation.
package store

import (
	"context"
	"errors"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// Row is one envelope as a backend sees it: the header it needs to index and
// guard writes, plus the canonical document bytes, opaque to the backend.
//
// Invariant: Table, ID, HLC and Deleted agree with Doc. Build it with
// FromRow so they always do; a backend stores and returns Doc unchanged,
// byte for byte.
type Row struct {
	Table   row.Table
	ID      ids.AnyID
	HLC     hlc.HLC
	Deleted bool
	Doc     []byte
}

// FromRow encodes r into a backend Row.
func FromRow(r row.Row) (Row, error) {
	doc, err := row.Encode(r)
	if err != nil {
		return Row{}, err
	}
	return Row{Table: r.Table, ID: r.ID, HLC: r.HLC, Deleted: r.Deleted, Doc: doc}, nil
}

// Decode parses the document back into a model row.
func (r Row) Decode() (row.Row, error) { return row.Decode(r.Doc) }

// Key identifies a row inside one store.
type Key struct {
	Table row.Table
	ID    ids.AnyID
}

// Key returns r's key.
func (r Row) Key() Key { return Key{Table: r.Table, ID: r.ID} }

// Cursor is opaque to the core; only the backend that issued it reads it.
// The zero value means "from the beginning", and a cursor the backend
// cannot read also means "from the beginning" (spec 5.2), so a rebuild is
// always correct, only slower.
type Cursor string

// Change is one checked write. Expect guards against lost updates: the
// backend applies the change only if the stored row's HLC still equals
// Expect (the zero HLC = the row must not exist yet), else ErrStale.
type Change struct {
	Row    Row
	Expect hlc.HLC
}

// Capabilities are optional features; the core uses one only when the
// backend reports it, and never asks a backend to pretend.
type Capabilities struct {
	History     bool // old versions of a row can be read back
	PushNotify  bool // the backend says when something changed
	RemoteQuery bool // queries can run without a local copy
	RowAccess   bool // access per row, not only per store
	Review      bool // changes can be proposed and reviewed first
	Remote      bool // Sync talks to something outside this machine
}

// Backend keeps one store's rows.
//
// Contract (enforced by storetest.Run): Get returns tombstones as rows (they
// are kept forever); Write is all-or-none and refuses a batch naming one row
// twice; every method after Close fails; Close is idempotent.
type Backend interface {
	// Read returns every row changed after cursor (tombstones included), in
	// any order, plus the cursor to pass next time.
	Read(ctx context.Context, cursor Cursor) ([]Row, Cursor, error)
	// Get returns one row by table and id, or ErrNotFound.
	Get(ctx context.Context, table row.Table, id ids.AnyID) (Row, error)
	// Write stores checked changes atomically as one batch: all or none.
	Write(ctx context.Context, changes []Change) error
	// Sync exchanges local changes with the backend's remote, if it has one,
	// and reports what moved. A backend without a remote returns an empty
	// report.
	Sync(ctx context.Context) (SyncReport, error)
	// Capabilities says which optional features this backend supports.
	Capabilities() Capabilities
	// Close releases locks and handles.
	Close() error
}

// SyncReport tells the core and the person what a Sync did.
type SyncReport struct {
	Sent     int         // local changes now on the remote
	Received int         // remote changes now local
	Merged   []ids.AnyID // rows merged field by field
	Refused  []Refusal
	Offline  bool // the remote could not be reached; nothing was lost
}

// Refusal is one change the remote did not accept.
type Refusal struct {
	ID     ids.AnyID
	Code   errs.Code
	Reason string
	// Folder is the data folder of the store that holds the row (a
	// backend with folders: rowfs folder name, "." for the root); "" when
	// the refusal names no row or the backend has no folders.
	Folder string
	// Held marks a refusal of a row in another folder of the same store
	// than the reporting backend's: one sync of the store's clone carries
	// every folder, so this folder's push did not run either, but the row
	// is that folder's to report (owner decision 7, 2026-11-10: a conflict
	// is reported once, under the folder that holds the row).
	Held bool
}

// Sentinel errors every backend returns (wrapped with detail, matched with
// errors.Is) for the same situations. Each carries its E_ code, so a surface
// maps it to an exit and HTTP status without a per-backend table.
var (
	ErrNotFound  error = errs.New(errs.NotFound, "store: row not found")
	ErrStale     error = errs.New(errs.Stale, "store: row changed since it was read")
	ErrReadOnly  error = errs.New(errs.StoreReadOnly, "store: attached read-only")
	ErrOffline   error = errs.New(errs.Offline, "store: remote unreachable")
	ErrLocked    error = errs.New(errs.StoreLocked, "store: another process holds the store lock")
	ErrNotAStore error = errs.New(errs.NotAStore, "store: url is not a lore store")
	ErrScheme    error = errs.New(errs.StoreScheme, "store: no backend for this url scheme")
)

// Sentinels whose code is shared with unrelated errors (E_INTERNAL,
// E_INVALID_INPUT) are plain errors, because *errs.Error matches by code
// alone: a backend returns them wrapped, errs.Wrap(code, msg, ErrClosed),
// so errors.Is finds exactly this cause and errs.CodeOf still gives the code.
var (
	// ErrClosed: a method was called after Close (wrap with errs.Internal).
	ErrClosed = errors.New("store: backend is closed")
	// ErrBatch: one batch names the same row twice (wrap with
	// errs.InvalidInput).
	ErrBatch = errors.New("store: a batch names the same row twice")
)

// Holder is an optional capability of a backend whose writes take a store
// lock (OV21/OV62: meta rebuild holds the lock across its reads, writes
// and syncs). Hold takes the lock and returns a ctx carrying it: every
// Write, Sync or other locking call made with that ctx (on this backend or
// another folder of the same store) reuses the held lock instead of
// waiting, so it cannot deadlock on itself. release drops it; it is
// idempotent. Holding with a ctx that already holds is a no-op whose
// release does nothing.
type Holder interface {
	Hold(ctx context.Context) (held context.Context, release func() error, err error)
}
