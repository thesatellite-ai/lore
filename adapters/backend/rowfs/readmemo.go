package rowfs

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/store"
)

// readMemo remembers, per row file, the decoded row of the last version
// Read saw, so a second Read in the same process does not read and decode
// an unchanged file again (BUG-TIME-BENCH: one command lists the store
// several times, and every full Read decoded every row file, so a write in
// a store of big task rows cost minutes).
//
// It lives only in memory on one Folder value: nothing is written to disk
// and nothing outlives the process. A file is "unchanged" by the same test
// a StatCursor uses (size and mtime equal and the mtime outside the racy
// window of the read that filled the entry), and a file whose stat changed
// is read again and reused only when its sha256 is the same.
type readMemo struct {
	mu   sync.Mutex
	rows map[string]memoRow
}

// memoRow is one remembered row file.
type memoRow struct {
	size, mtime int64
	at          int64 // when the Read that filled the entry started
	sha         string
	row         store.Row
}

// fresh returns the entry of rel when the scanned stat st still matches it.
func (m *readMemo) fresh(rel string, st entry) (memoRow, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.rows[rel]
	if !ok || e.size != st.size || e.mtime != st.mtime || racy(e.mtime, e.at) {
		return memoRow{}, false
	}
	return e, true
}

// bySHA returns the entry of rel when its content hash is sha.
func (m *readMemo) bySHA(rel, sha string) (memoRow, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.rows[rel]
	return e, ok && e.sha == sha
}

// put records rel's decoded row.
func (m *readMemo) put(rel string, e memoRow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]memoRow{}
	}
	m.rows[rel] = e
}

// checked is one file's result of Folder.check.
type checked struct {
	stat FileStat
	row  *store.Row
	ok   bool
	// bad is why the file is not a usable row (nil for a good file or one
	// gone since the scan); Read keeps it, with its code, for ReadProblems.
	bad error
}

// parallel runs work(0..n-1) on up to GOMAXPROCS goroutines and returns
// ctx's error when ctx ends first (work items already started finish; the
// rest are skipped).
func parallel(ctx context.Context, n int, work func(i int)) error {
	workers := min(runtime.GOMAXPROCS(0), n)
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				work(i)
			}
		}()
	}
	wg.Wait()
	return ctx.Err()
}
