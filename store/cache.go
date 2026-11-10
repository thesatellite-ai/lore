package store

import "context"

// Source names one attached store inside a cache: the store's name in
// .lore/stores.yaml, or SourceRepo for the repo's own rows. It becomes the
// lens's `source` column (spec 7.4), so it must be stable across runs.
type Source string

// SourceRepo is the repo's own store (.lore/data).
const SourceRepo Source = "repo"

// Cache is the port of the local, rebuildable copy that search, list, sql
// and render read (spec 3, 7.4). The engine calls Apply after every backend
// write and on refresh (Backend.Read since Cursor(source)); a cache never
// holds the truth, so deleting it loses nothing.
//
// The adapter's constructor takes the table registry (*schema.Registry) to
// build typed tables; the port itself needs only envelopes, which is why it
// lives here in L2 without importing schema.
type Cache interface {
	// Apply upserts rows of one source (tombstones included: a tombstone
	// removes the row from typed tables but is remembered so an older copy
	// cannot come back) and records next as that source's cursor, in one
	// transaction: the rows and the cursor are committed together or not at
	// all, so a crash can never leave a cursor ahead of its rows.
	Apply(ctx context.Context, source Source, rows []Row, next Cursor) error
	// Cursor returns the source's recorded cursor ("" when never applied).
	Cursor(ctx context.Context, source Source) (Cursor, error)
	// Rebuild drops every row and the cursor of source, so the next Read
	// with the empty cursor refills it from the backend.
	Rebuild(ctx context.Context, source Source) error
	// Close releases the database handle; idempotent.
	Close() error
}
