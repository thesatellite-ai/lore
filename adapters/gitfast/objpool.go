package gitfast

// objpool.go — object stores kept across commands by a long-lived lore
// process (devco:D-838, the local read server of D-833).
//
// Why: a one-shot lore opens each repository's object folder once per
// process, and that is once per command. The read server runs many
// commands in one process, and each opened the object folders afresh, so
// every served `lore ask` inflated the same packed row documents again
// (~30% of the server's time per call on a 6,000-row store). An object's
// content is fixed by its id, so a store's decoded objects stay true for
// as long as the process lives; a lookup that misses already re-reads the
// pack list (readLocked), so objects written since are found too.
//
// A kept store is dropped (and its packs closed) when one of its packs is
// gone (a repack: its open file would pin the deleted pack's disk space),
// when the alternates file changed, or when it holds more than
// objPoolMaxObjects decoded objects.

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// objPoolMaxObjects bounds the decoded objects one kept store holds
// before it is dropped and opened afresh.
const objPoolMaxObjects = 200_000

// objPool is the process's kept object stores (off unless
// ShareObjectStores turned it on).
var objPool struct {
	mu     sync.Mutex
	on     bool
	stores map[string]*keptStore // objects folder -> store
}

// keptStore is one kept object store and the alternates file it was
// opened with.
type keptStore struct {
	s   *objectStore
	alt string // fileStamp of objects/info/alternates
}

// ShareObjectStores makes every Session of this process share one object
// store per object folder, kept across Sessions, until stop. Only a
// process that runs many commands calls it (the read server); stop closes
// every kept store.
func ShareObjectStores() (stop func()) {
	objPool.mu.Lock()
	objPool.on = true
	objPool.stores = map[string]*keptStore{}
	objPool.mu.Unlock()
	return func() {
		objPool.mu.Lock()
		defer objPool.mu.Unlock()
		for _, k := range objPool.stores {
			k.s.close()
		}
		objPool.on, objPool.stores = false, nil
	}
}

// openObjectStore is the object store of the folder dir: the kept one
// when stores are shared and it is still current, else a new one (kept
// when shared). shared tells the caller not to close it.
func openObjectStore(dir string) (s *objectStore, shared bool, err error) {
	objPool.mu.Lock()
	defer objPool.mu.Unlock()
	if !objPool.on {
		s, err = newObjectStore(dir)
		return s, false, err
	}
	alt := fileStamp(filepath.Join(dir, "info", "alternates"))
	if k, ok := objPool.stores[dir]; ok {
		if k.alt == alt && k.s.current() {
			return k.s, true, nil
		}
		k.s.close()
		delete(objPool.stores, dir)
	}
	s, err = newObjectStore(dir)
	if err != nil {
		return nil, false, err
	}
	objPool.stores[dir] = &keptStore{s: s, alt: alt}
	return s, true, nil
}

// current reports whether every pack the store opened is still there and
// it holds no more than objPoolMaxObjects decoded objects.
func (s *objectStore) current() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) > objPoolMaxObjects {
		return false
	}
	for _, p := range s.packs {
		if _, err := os.Stat(p.path); err != nil {
			return false
		}
	}
	return true
}

// fileStamp is path's size and modification time ("" when missing).
func fileStamp(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(fi.Size(), 10) + ":" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}
