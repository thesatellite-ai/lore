package engine

// readsnapbudget.go — a read snapshot keeps at most snapshotBudget bytes
// of rows (bug hunt H-280, devco:D-1018). A doctor run shares one
// snapshot, and it kept each store's full read and each table read for the
// whole run, with every row it decoded: on a compacted store of 1.26M row
// files in 213 folders, the open that builds the cache read every folder
// through it and held every row of the store (~1 GB) until doctor ended.
// A snapshot whose reads fit the budget keeps them all, as before; past
// it the reads used longest ago are dropped (a later reader reads that
// store again) and the decode memo takes no more rows.

import (
	"github.com/thesatellite-ai/lore/store"
)

// snapshotBudget is the most row bytes (documents plus snapRowBytes a
// row) a read snapshot keeps in its full and table reads. A var: a test
// sets a small one.
var snapshotBudget int64 = 256 << 20

// decodeMemoBudget is the most a snapshot's decode memo keeps
// (decodedCost a row). A var: a test sets a small one.
var decodeMemoBudget int64 = 192 << 20

// snapRowBytes is what a kept row costs besides its document: the row in
// the fill, again in its by-table grouping, and the backend's own copy.
const snapRowBytes = 384

// snapKept is one finished read a snapshot keeps, least recently used
// first: a full read of full, or (table set) the read of one table.
type snapKept struct {
	full  store.Backend
	table *tableFillKey
	f     *snapFill
	size  int64
}

// snapRowsBytes is what rows cost kept.
func snapRowsBytes(rows []store.Row) int64 {
	var n int64
	for _, r := range rows {
		n += int64(len(r.Doc)) + snapRowBytes
	}
	return n
}

// keepFill records f, a finished read (of full, or of table when table is
// set), as the newest the snapshot keeps; past snapshotBudget the oldest
// others are dropped. The caller does not hold s.mu.
func (s *readSnapshot) keepFill(full store.Backend, table *tableFillKey, f *snapFill) {
	size := snapRowsBytes(f.rows)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kept = append(s.kept, snapKept{full: full, table: table, f: f, size: size})
	s.keptBytes += size
	for s.keptBytes > snapshotBudget && len(s.kept) > 1 {
		old := s.kept[0]
		s.kept = s.kept[1:]
		s.keptBytes -= old.size
		s.forgetLocked(old)
	}
}

// forgetLocked drops k's read from s, unless a newer read replaced it;
// the caller holds s.mu.
func (s *readSnapshot) forgetLocked(k snapKept) {
	if k.table != nil {
		if s.tables[*k.table] == k.f {
			delete(s.tables, *k.table)
		}
		return
	}
	if s.full[k.full] == k.f {
		delete(s.full, k.full)
	}
}

// touch makes f, a kept read a reader just used, the most recently used:
// a store every check reads is dropped last.
func (s *readSnapshot) touch(f *snapFill) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, k := range s.kept {
		if k.f == f {
			s.kept = append(append(s.kept[:i:i], s.kept[i+1:]...), k)
			return
		}
	}
}

// unkeepLocked takes every kept read of backend b out of the budget (its
// reads were dropped); the caller holds s.mu.
func (s *readSnapshot) unkeepLocked(b store.Backend) {
	out := s.kept[:0]
	for _, k := range s.kept {
		if k.full == b || (k.table != nil && k.table.b == b) {
			s.keptBytes -= k.size
			continue
		}
		out = append(out, k)
	}
	s.kept = out
}

// decodedCost is what a decoded row of an n-byte document is counted as
// in the decode memo: its text in the row's values, the decoded
// structure taken as the text again, and the entry (model/row memoCost).
func decodedCost(n int) int64 { return 2*int64(n) + 512 }

// memoLocked reports whether a decode of doc still fits the memo's
// budget, and counts it when it does; the caller holds s.mu. A full memo
// keeps what it holds and takes no more: doctor's checks list every
// table again and again, and a memo that started over when full served
// almost nothing to reads that cycle through more rows than it holds.
func (s *readSnapshot) memoLocked(doc []byte) bool {
	cost := decodedCost(len(doc))
	if s.decodedBytes+cost > decodeMemoBudget {
		return false
	}
	s.decodedBytes += cost
	return true
}
