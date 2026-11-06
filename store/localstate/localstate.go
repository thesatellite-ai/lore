// Package localstate is the port of this machine's local state: data that
// is per machine and never synced, never committed, never in a row file
// (FA-OLD-LOCAL; SPEC 10.4 Q-LOCAL). It replaces lore.db's ent tables for
// new core features; the adapter (adapters/cache/sqlite LocalState) keeps
// one SQLite file at <lore home>/state/local.db, its tables created at run
// time from the closed list below (no code generation).
//
// Flow:
//
//	ops/<noun> ──Env.LocalState(ctx)──▶ wire (opens once per process)
//	   ──▶ Store.Put/Get/List/Delete(table, key, JSON doc)
//	   ──▶ adapters/cache/sqlite: CREATE TABLE IF NOT EXISTS <table>(key, doc, updated_at)
//
// Each table is a key → JSON document map. The caller owns the document's
// typed shape (PutJSON / GetJSON with its own struct) and the key's format
// (e.g. "<sto_ id>/<row id>"); a new table is a new constant here.
package localstate

import (
	"context"
	"encoding/json"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Table names one machine-local table. Closed set: Tables lists them.
type Table string

// The machine-local tables (FA-OLD-LOCAL).
const (
	// Usage records which rows `lore context` and the hooks served
	// (CONTEXT 6.8 decay buffer): key = row id, doc = served count and
	// last served time. Private by design: it never travels.
	Usage Table = "row_usage"
	// SecretChecks keeps the last `lore secrets --missing` result per
	// vault entry (SEC 2.6, SEC-IDX1): vault reachability is per machine.
	SecretChecks Table = "secret_checks"
	// TimeOutbox queues run time spans for a task whose store was
	// unreachable (TIME 4.3, 11.9), replayed on the next reachable write;
	// key = span id, so a replay is idempotent.
	TimeOutbox Table = "time_outbox"
	// StorePushRefusals records the last push of a store clone that the
	// remote refused (a server-side hook, branch protection, permission):
	// key = store name, doc = the rows that wait, the reason and when.
	// `lore sync status` lists it; a later push that lands clears it.
	StorePushRefusals Table = "store_push_refusals"
)

// Tables is every machine-local table, created at open.
func Tables() []Table { return []Table{Usage, SecretChecks, TimeOutbox, StorePushRefusals} }

// Valid reports whether t is one of Tables.
func (t Table) Valid() bool {
	for _, k := range Tables() {
		if k == t {
			return true
		}
	}
	return false
}

// Entry is one stored document.
type Entry struct {
	Key       string
	Doc       []byte // JSON
	UpdatedAt time.Time
}

// Store is the machine-local state port. Every method refuses a table not
// in Tables (E_INVALID_INPUT) and an empty key.
type Store interface {
	// Put inserts or replaces the document at key.
	Put(ctx context.Context, t Table, key string, doc []byte) error
	// Get returns the document at key; ok=false when none.
	Get(ctx context.Context, t Table, key string) (e Entry, ok bool, err error)
	// List returns the entries whose key starts with prefix ("" = all),
	// ordered by key.
	List(ctx context.Context, t Table, prefix string) ([]Entry, error)
	// Delete removes the document at key; a missing key is not an error.
	Delete(ctx context.Context, t Table, key string) error
	// Close releases the file.
	Close() error
}

// PutJSON encodes v and stores it at key.
func PutJSON[T any](ctx context.Context, s Store, t Table, key string, v T) error {
	b, err := json.Marshal(v)
	if err != nil {
		return errs.Wrap(errs.Internal, "localstate: encode "+string(t)+" "+key, err)
	}
	return s.Put(ctx, t, key, b)
}

// GetJSON reads the document at key into a T; ok=false when none.
func GetJSON[T any](ctx context.Context, s Store, t Table, key string) (v T, ok bool, err error) {
	e, ok, err := s.Get(ctx, t, key)
	if err != nil || !ok {
		return v, ok, err
	}
	if uerr := json.Unmarshal(e.Doc, &v); uerr != nil {
		return v, false, errs.Wrap(errs.Internal, "localstate: decode "+string(t)+" "+key, uerr)
	}
	return v, true, nil
}

// Check is the shared argument check of every adapter.
func Check(t Table, key string) error {
	if !t.Valid() {
		return errs.New(errs.InvalidInput, "localstate: unknown table "+string(t))
	}
	if key == "" {
		return errs.New(errs.InvalidInput, "localstate: empty key")
	}
	return nil
}
