package row

import (
	"maps"
	"sync"
)

// decodememo.go — Decode remembers the rows it decoded, keyed by the exact
// document bytes, so one process decodes each row file once. A command
// reads the same files many times: the row folder decodes every file it
// lists, the engine decodes the bytes again for each List, and a list
// command's merged check, owner index, forest and view, or doctor's ~25
// checks, each list the same tables. On a 5,500-row repo that was the
// largest share of a warm `task list` and of `lore doctor` (row.Decode
// 0.75 s of 2.2 s CPU, 3.9 s of 9.4 s).
//
// Decode is a pure function of the bytes (the step registry is fixed at
// build time), so a hit can never be stale, in a CLI run or a long-lived
// server alike. Values are immutable; the row's three maps are the only
// shared mutable parts, so every hit hands out its own copies of them.
// Only successful decodes are kept. The memo is bounded by the bytes of the
// documents it holds: past decodeMemoMaxBytes it starts over, which keeps a
// server that sees many row versions from growing without end.

// decodeMemoMaxBytes bounds the document bytes the memo keeps (a 5,500-row
// repo's .lore/data is ~23 MB).
const decodeMemoMaxBytes = 48 << 20

// decodeMemoMinBytes skips the memo for tiny documents: below it a decode
// costs about what the lookup and copies do.
const decodeMemoMinBytes = 64

// decodeMemo holds decoded rows by document; safe for concurrent use
// (doctor runs its checks in parallel).
type decodeMemo struct {
	mu    sync.Mutex
	rows  map[string]Row
	bytes int
}

// memo is the process's decode memo.
var memo = &decodeMemo{rows: map[string]Row{}}

// get returns a private copy of the row decoded from doc, if kept.
func (m *decodeMemo) get(doc []byte) (Row, bool) {
	m.mu.Lock()
	r, ok := m.rows[string(doc)] // no allocation: the conversion is only a lookup key
	m.mu.Unlock()
	if !ok {
		return Row{}, false
	}
	return r.ownMaps(), true
}

// put keeps r as the decode of doc. r must not be used by anyone else
// afterwards except through ownMaps copies: put stores its own copy.
func (m *decodeMemo) put(doc []byte, r Row) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[string(doc)]; ok {
		return
	}
	if m.bytes+len(doc) > decodeMemoMaxBytes {
		clear(m.rows)
		m.bytes = 0
	}
	m.rows[string(doc)] = r.ownMaps()
	m.bytes += len(doc)
}

// ownMaps is r with its own copies of Meta, Fields and Reserved (nil stays
// nil), so a caller that edits a decoded row's maps changes no other copy.
func (r Row) ownMaps() Row {
	if r.Meta != nil {
		r.Meta = maps.Clone(r.Meta)
	}
	if r.Fields != nil {
		r.Fields = maps.Clone(r.Fields)
	}
	if r.Reserved != nil {
		r.Reserved = maps.Clone(r.Reserved)
	}
	return r
}
