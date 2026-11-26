package row

import (
	"hash/maphash"
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
// Only successful decodes are kept.
//
// The memo is bounded by what it holds: past decodeMemoMaxBytes it starts
// over, which keeps a server that sees many row versions from growing
// without end. A document is named by two 64-bit hashes of its bytes under
// seeds chosen at random per process (memoKey), not by a copy of the
// bytes. Why (bug hunt H-277, devco:D-1015): the memo kept every document
// twice (the key and the decoded row's text) and counted only the key:
// ~145 MB of lore doctor's memory on a 100k-commit store.

// decodeMemoMaxBytes bounds what the memo holds, by memoCost (a 5,500-row
// repo's .lore/data is ~23 MB: it still fits whole).
const decodeMemoMaxBytes = 64 << 20

// decodeMemoMinBytes skips the memo for tiny documents: below it a decode
// costs about what the lookup and copies do.
const decodeMemoMinBytes = 64

// memoEntryOverhead is what one kept row costs beyond its text: the row,
// its three maps and the map entry.
const memoEntryOverhead = 512

// memoCost is the memory a decoded document of n bytes is counted as: its
// text held once in the row's values (the parse keeps every string it
// read) plus the decoded structure, taken as the text again, and the
// entry's overhead.
func memoCost(n int) int { return 2*n + memoEntryOverhead }

// memoKey names a document by two hashes of its bytes. The seeds are
// random per process, so no document can be made to collide with another
// (a 128-bit accidental collision is beyond any store's size).
type memoKey struct{ a, b uint64 }

// memoSeeds are the process's two hash seeds.
var memoSeeds = [2]maphash.Seed{maphash.MakeSeed(), maphash.MakeSeed()}

// keyOf is doc's memo key.
func keyOf(doc []byte) memoKey {
	return memoKey{a: maphash.Bytes(memoSeeds[0], doc), b: maphash.Bytes(memoSeeds[1], doc)}
}

// decodeMemo holds decoded rows by document; safe for concurrent use
// (doctor runs its checks in parallel).
type decodeMemo struct {
	mu    sync.Mutex
	rows  map[memoKey]Row
	bytes int
}

// memo is the process's decode memo.
var memo = &decodeMemo{rows: map[memoKey]Row{}}

// get returns a private copy of the row decoded from doc, if kept.
func (m *decodeMemo) get(doc []byte) (Row, bool) {
	k := keyOf(doc)
	m.mu.Lock()
	r, ok := m.rows[k]
	m.mu.Unlock()
	if !ok {
		return Row{}, false
	}
	return r.ownMaps(), true
}

// put keeps r as the decode of doc. r must not be used by anyone else
// afterwards except through ownMaps copies: put stores its own copy.
func (m *decodeMemo) put(doc []byte, r Row) {
	k, cost := keyOf(doc), memoCost(len(doc))
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[k]; ok {
		return
	}
	if m.bytes+cost > decodeMemoMaxBytes {
		clear(m.rows)
		m.bytes = 0
	}
	m.rows[k] = r.ownMaps()
	m.bytes += cost
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
