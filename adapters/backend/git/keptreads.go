package git

// keptreads.go — the reads every folder's backend keeps (its last full
// read and its table reads, Backend.fullRows and tableRows) share one
// budget per process (bug hunt H-280, devco:D-1018). Each backend kept
// its reads for the whole process, so a command that reads every folder
// of a big store held every row file of it: lore doctor's first run on a
// compacted store of 1.26M row files (213 folders) built its cache folder
// by folder and grew past 1 GB on the kept reads alone. A store whose
// kept reads fit keptReadBudget keeps them all, as before; past it the
// backend that kept its reads longest ago drops them, and its next read
// asks git again. The backend whose reads were used longest ago drops
// first: a folder every command reads stays.

import (
	"runtime"
	"sync"
	"weak"

	"github.com/thesatellite-ai/lore/store"
)

// keptReadBudget is the most row bytes (documents plus keptRowBytes a
// row) the kept reads of all backends of a process hold. A var: a test
// sets a small one.
var keptReadBudget int64 = 192 << 20

// keptRowBytes is what a kept row costs besides its document: the row
// itself (table, id, stamp, document header) in the kept slice.
const keptRowBytes = 128

// keptReads is the process's kept reads: each backend's size and the
// order they were kept in (oldest first). Backends are held weakly: a
// backend no one uses any more is collected with its reads, and its entry
// goes with it (runtime.AddCleanup).
var keptReads = struct {
	sync.Mutex
	size  map[weak.Pointer[Backend]]int64
	order []weak.Pointer[Backend]
	total int64
}{size: map[weak.Pointer[Backend]]int64{}}

// rowsBytes is what rows cost kept.
func rowsBytes(rows []store.Row) int64 {
	var n int64
	for _, r := range rows {
		n += int64(len(r.Doc)) + keptRowBytes
	}
	return n
}

// keptBytes is what b's kept reads hold now; the caller holds b.fullMu.
func (b *Backend) keptBytes() int64 {
	n := rowsBytes(b.fullRows)
	for _, rows := range b.tableRows {
		n += rowsBytes(rows)
	}
	return n
}

// noteKept records that b now keeps n bytes and makes it the newest;
// past keptReadBudget the other backends used longest ago drop their reads. The
// caller must not hold b.fullMu (a dropped backend's lock is taken here).
func noteKept(b *Backend, n int64) {
	wp := weak.Make(b)
	keptReads.Lock()
	defer keptReads.Unlock()
	was, known := keptReads.size[wp]
	if !known {
		runtime.AddCleanup(b, forgetKeptRef, wp)
	}
	keptReads.total += n - was
	keptReads.size[wp] = n
	unorder(wp)
	keptReads.order = append(keptReads.order, wp)
	for keptReads.total > keptReadBudget && len(keptReads.order) > 1 {
		oldRef := keptReads.order[0]
		keptReads.order = keptReads.order[1:]
		keptReads.total -= keptReads.size[oldRef]
		keptReads.size[oldRef] = 0 // still known: its cleanup is registered
		if old := oldRef.Value(); old != nil {
			old.fullMu.Lock()
			old.fullHead, old.fullRows = "", nil
			old.tableHead, old.tableRows = "", nil
			old.fullMu.Unlock()
		}
	}
}

// unorder takes wp out of the kept order; the caller holds keptReads.
func unorder(wp weak.Pointer[Backend]) {
	for i, o := range keptReads.order {
		if o == wp {
			keptReads.order = append(keptReads.order[:i], keptReads.order[i+1:]...)
			return
		}
	}
}

// touchKept makes b, whose kept read was just used, the most recently
// used backend.
func touchKept(b *Backend) {
	wp := weak.Make(b)
	keptReads.Lock()
	defer keptReads.Unlock()
	if _, known := keptReads.size[wp]; !known {
		return
	}
	unorder(wp)
	keptReads.order = append(keptReads.order, wp)
}

// forgetKept removes b from the budget (its reads were dropped).
func forgetKept(b *Backend) { forgetKeptRef(weak.Make(b)) }

// forgetKeptRef removes the backend wp from the budget.
func forgetKeptRef(wp weak.Pointer[Backend]) {
	keptReads.Lock()
	defer keptReads.Unlock()
	keptReads.total -= keptReads.size[wp]
	delete(keptReads.size, wp)
	unorder(wp)
}
