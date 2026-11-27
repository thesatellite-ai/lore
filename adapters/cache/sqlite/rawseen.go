package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"hash/maphash"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/store"
)

// rawSeen lets keepRaw skip the two lookups of a row no copy of which can
// be held: when the source held no row as the Apply began, a row's copy in
// `_rows` can only be one this Apply stored, and when no other source held
// a row either, the same goes for its id anywhere (idHeld). It keeps a
// 64-bit hash of every id the Apply stored, never the id itself: a hash
// seen before (the same id twice, or rarely another id with the same
// hash) runs the lookups as always, so the answers are exact.
//
// Why (bug hunt H-285, devco:D-1023): a cache built from nothing ran both
// lookups for each of its rows, and each missed; on a 1.26M-row store
// they were a quarter of the one cache writer's time.
type rawSeen struct {
	// srcEmpty: the source held no row as the Apply began; othersEmpty:
	// no other source did either.
	srcEmpty, othersEmpty bool
	seed                  maphash.Seed
	ids                   map[uint64]struct{}
}

// heldLookups counts the lookups keepRaw ran (its held copy and idHeld),
// so a test can tell that a build from nothing runs none (H-285).
var heldLookups atomic.Int64

// newRawSeen reads, in tx, whether source and the other sources hold any
// row. It returns nil (every lookup runs) when source holds rows already.
func newRawSeen(ctx context.Context, tx *sql.Tx, source store.Source) (*rawSeen, error) {
	src, err := anyRow(ctx, tx, `SELECT 1 FROM "`+tblRows+`" WHERE source = ? LIMIT 1`, source)
	if err != nil || src {
		return nil, err
	}
	others, err := anyRow(ctx, tx, `SELECT 1 FROM "`+tblRows+`" WHERE source <> ? LIMIT 1`, source)
	if err != nil {
		return nil, err
	}
	return &rawSeen{srcEmpty: true, othersEmpty: !others, seed: maphash.MakeSeed(), ids: map[uint64]struct{}{}}, nil
}

// anyRow runs a one-row probe q with source.
func anyRow(ctx context.Context, tx *sql.Tx, q string, source store.Source) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, q, string(source)).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, dbErr("find held rows", err)
	}
	return true, nil
}

// none reports that the source cannot hold a copy of id: it held no row
// as the Apply began and the Apply has not stored id.
func (s *rawSeen) none(id string) bool {
	return s != nil && s.srcEmpty && !s.has(id)
}

// noneElsewhere reports that no source can hold a copy of id.
func (s *rawSeen) noneElsewhere(id string) bool {
	return s != nil && s.othersEmpty && !s.has(id)
}

// has reports whether the Apply stored id (or an id with its hash).
func (s *rawSeen) has(id string) bool {
	_, ok := s.ids[maphash.String(s.seed, id)]
	return ok
}

// add records that the Apply stored id.
func (s *rawSeen) add(id string) {
	if s != nil {
		s.ids[maphash.String(s.seed, id)] = struct{}{}
	}
}
