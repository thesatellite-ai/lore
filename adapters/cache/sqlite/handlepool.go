package sqlite

// handlepool.go — the read server keeps folder cache handles open between
// the commands it serves (devco:D-890).
//
// Why: a served read opened every folder cache again on every call. A new
// connection makes SQLite parse the file's whole schema (~700 objects): with
// a stale open stamp (after an upgrade, until a write restamps the folder)
// that parse was most of a warm `lore ask`, and even with a current stamp
// the first statement that needs the tables (ensureInit) pays it. A
// long-lived process keeps the connection, whose schema SQLite already
// parsed:
//
//	openCache ─▶ reuse on? idle handle for this path?
//	   ├─ same file (device+inode) ─▶ PRAGMA data_version and
//	   │  schema_version on the live connection, and the registry's id,
//	   │  against what they were when the handle went idle:
//	   │     all equal ─▶ reused as is
//	   │     any moved ─▶ init on the live connection (what a fresh open
//	   │                  runs, minus the connect), then reused
//	   └─ else the normal open
//	Cache.Close ─▶ a read handle goes back to idle with the versions it saw;
//	               a handle that committed closes for real (its close
//	               restamps the file, stamp.go), as does every handle while
//	               reuse is off
//
// Correctness: the connection always reads the file's current committed
// data (SQLite checks the WAL index on every read transaction), so rows
// another process wrote are seen. What the handle keeps in Go (st: the
// table plans and kinds) is rebuilt by init whenever another connection
// committed anything (data_version) or changed the schema, so it is never
// older than the file. A file replaced on disk (a rebuild, a healed or
// deleted cache: new inode, or gone) is never reused. Off unless the read
// server turned it on: a one-shot process never keeps a handle.

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/schema"
)

// handlePoolMax is how many idle cache handles the pool keeps (one per
// folder cache; a person's home has a few dozen at most).
const handlePoolMax = 64

var handlePool struct {
	mu   sync.Mutex
	on   bool
	idle map[string]*idleHandle // by absolute path
}

// handleStats counts what takeHandle did; tests read them to prove a
// handle was (or was not) reused.
var handleStats struct{ hits, refreshed, misses atomic.Int64 }

// idleHandle is an idle Cache and what its file was when it went idle.
type idleHandle struct {
	c             *Cache
	identity      string // device+inode (fileIdentity)
	data, schemaV int64  // PRAGMA data_version / schema_version (connected handles)
	reg           string // regID of the registry init ran with
	lazy          bool   // never connected: reusable only while its stamp is current
}

// EnableCacheReuse turns cache handle reuse on for this process (the read
// server only) and returns the func that turns it off and closes every idle
// handle.
func EnableCacheReuse() func() {
	handlePool.mu.Lock()
	handlePool.on = true
	handlePool.idle = map[string]*idleHandle{}
	handlePool.mu.Unlock()
	return func() {
		handlePool.mu.Lock()
		idle := handlePool.idle
		handlePool.idle, handlePool.on = nil, false
		handlePool.mu.Unlock()
		for _, h := range idle {
			h.c.closeIdle()
		}
	}
}

// takeHandle returns the idle handle of path made current for reg and exp,
// nil when there is none usable (the caller opens normally).
func takeHandle(ctx context.Context, path string, reg *schema.Registry, exp Expander, free freeBytesFunc) *Cache {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	handlePool.mu.Lock()
	if !handlePool.on {
		handlePool.mu.Unlock()
		return nil
	}
	h := handlePool.idle[abs]
	delete(handlePool.idle, abs)
	handlePool.mu.Unlock()
	if h == nil {
		handleStats.misses.Add(1)
		return nil
	}
	if fi, err := os.Stat(abs); err != nil || fileIdentity(fi) == "" || fileIdentity(fi) != h.identity {
		h.c.closeIdle() // deleted, rebuilt or replaced: another file now
		handleStats.misses.Add(1)
		return nil
	}
	c := h.c
	c.mu.Lock()
	defer c.mu.Unlock()
	c.base, c.exp, c.writes.free = reg, exp, free
	c.closed, c.committed, c.adoptStamp, c.unfoldReads = false, false, false, 0
	if h.lazy {
		st, fit := readStamp(path, reg)
		if fit != stampCurrent {
			c.closed = true
			_ = c.db.Close() // never connected: nothing to fold, nothing to restamp
			handleStats.misses.Add(1)
			return nil
		}
		c.lazy = &st
		handleStats.hits.Add(1)
		return c
	}
	data, schemaV, err := liveVersions(ctx, c)
	if err != nil {
		c.closed = true
		_ = c.db.Close()
		handleStats.misses.Add(1)
		return nil
	}
	if data == h.data && schemaV == h.schemaV && h.reg == regID(reg) {
		handleStats.hits.Add(1)
		return c
	}
	if err := c.init(ctx); err != nil {
		c.closed = true
		_ = c.db.Close()
		handleStats.misses.Add(1)
		return nil
	}
	handleStats.refreshed.Add(1)
	return c
}

// liveVersions reads PRAGMA data_version and schema_version on c's
// connection (c.mu held). data_version moves when another connection
// committed; schema_version when anyone changed the schema.
func liveVersions(ctx context.Context, c *Cache) (data, schemaV int64, err error) {
	if err = c.db.QueryRowContext(ctx, "PRAGMA data_version").Scan(&data); err != nil {
		return 0, 0, err
	}
	if err = c.db.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&schemaV); err != nil {
		return 0, 0, err
	}
	return data, schemaV, nil
}

// putHandle keeps c idle for the next command; false when it must close for
// real: reuse off, a handle that committed (its close folds the WAL and
// restamps the file), a file it cannot name, or a full pool. c.mu held.
func putHandle(ctx context.Context, c *Cache) bool {
	if c.committed || c.writes.path == "" {
		return false
	}
	handlePool.mu.Lock()
	on := handlePool.on
	handlePool.mu.Unlock()
	if !on {
		return false
	}
	abs, err := filepath.Abs(c.writes.path)
	if err != nil {
		return false
	}
	fi, err := os.Stat(abs)
	if err != nil || fileIdentity(fi) == "" {
		return false
	}
	h := &idleHandle{c: c, identity: fileIdentity(fi), lazy: c.lazy != nil}
	if !h.lazy {
		if h.data, h.schemaV, err = liveVersions(ctx, c); err != nil {
			return false
		}
		h.reg = regID(c.base)
	}
	handlePool.mu.Lock()
	if !handlePool.on {
		handlePool.mu.Unlock()
		return false
	}
	var evict *Cache
	if old := handlePool.idle[abs]; old != nil {
		evict = old.c // one idle handle per file: the newest wins
	} else if len(handlePool.idle) >= handlePoolMax {
		handlePool.mu.Unlock()
		return false
	}
	handlePool.idle[abs] = h
	handlePool.mu.Unlock()
	if evict != nil {
		evict.closeIdle()
	}
	return true
}

// closeIdle closes the connection of a handle the pool held (it committed
// nothing: Close's restamp does not apply).
func (c *Cache) closeIdle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	_ = c.db.Close()
}
