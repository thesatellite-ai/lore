package wire

// statepool.go — the read server keeps a store-only open across the calls
// it serves (devco:D-890).
//
// Why: with the folder cache handles kept (adapters/cache/sqlite
// handlepool.go) a warm served `lore ask --store me` still spent most of
// its time opening the store on every call (openDetachedHere: the git
// backend of each folder, the caches, the refresh from git, the actor and
// its checks over the store's rows, the machine store). A read changes
// none of it, so the read server keeps the opened state and hands it to
// the next call with the same inputs while nothing it was built from
// changed:
//
//	openStoreOnlyOn ─▶ reuse on? key = home, repo dir, the store(s), the
//	   │               whole environment (actor, LORE_NOW, switches)
//	   ├─ idle state with the key, the same fingerprint, < 30 s old ─▶ reused
//	   └─ else openDetachedHere (the normal open, refresh included)
//	App.Close ─▶ a kept state that wrote nothing goes back idle with the
//	             fingerprint taken now (after this call's own refresh)
//
// The fingerprint is the size and time of every file lore builds that
// state from: under the lore home config.yaml, state/* (each entry, not
// the files below: audit and batch folders grow by a file per write and
// a read reads none), cache/** (the folder caches and their WAL: any
// commit by any process moves them), each store clone's HEAD, index,
// packed-refs, FETCH_HEAD, ORIG_HEAD and refs/** (a write, a sync or a
// plain-git fast-forward moves one), and the repo dir's .lore entries.
// Any change means a full open, exactly as one-shot. The fingerprint must
// stay on: a kept open skips the refresh from git, so without it a row
// that arrived by sync was never seen (the exploration measured 0 of 3).
// The 30 s bound is the most a kept open may delay a fetch_ttl fetch (an
// open is where a store clone fetches).
// Off unless the read server turned it on: a one-shot process never keeps
// a state.

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/store"
)

// stateMaxAge bounds how long a kept open is handed out (see the top of
// this file).
const stateMaxAge = 30 * time.Second

// stateIdleMax is how many kept opens the pool holds (one per store and
// environment; a person asks a handful of stores).
const stateIdleMax = 8

var statePool struct {
	mu   sync.Mutex
	on   bool
	idle map[string]*idleState
}

// stateStats counts what takeState did; tests read them to prove a state
// was (or was not) reused.
var stateStats struct{ hits, misses, stale atomic.Int64 }

// idleState is a kept open, the fingerprint of the files it was built
// from and when it was opened.
type idleState struct {
	st *state
	fp string
	at time.Time
}

// EnableStateReuse turns store-only state reuse on for this process (the
// read server only) and returns the func that turns it off and closes
// every idle state.
func EnableStateReuse() func() {
	statePool.mu.Lock()
	statePool.on, statePool.idle = true, map[string]*idleState{}
	statePool.mu.Unlock()
	return func() {
		statePool.mu.Lock()
		idle := statePool.idle
		statePool.on, statePool.idle = false, nil
		statePool.mu.Unlock()
		for _, p := range idle {
			_ = p.st.close()
		}
	}
}

func stateReuseOn() bool {
	statePool.mu.Lock()
	defer statePool.mu.Unlock()
	return statePool.on
}

// stateKey names every input of a store-only open: the home, the repo
// dir, the folder it reads from, each store, and the whole environment
// (the read server runs each call with its client's environment).
func stateKey(opts Options, one attach.Store, here store.Source, also []attach.Store) string {
	var b strings.Builder
	b.WriteString("home\x00" + opts.Home + "\x01repo\x00" + opts.RepoDir + "\x01here\x00" + string(here) + "\x01")
	for _, s := range append([]attach.Store{one}, also...) {
		b.WriteString("store\x00" + string(s.Name) + "\x00" + s.URL + "\x00" + s.Folder + "\x00" + s.Branch + "\x01")
	}
	env := slices.Clone(os.Environ())
	slices.Sort(env)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "_=") { // the shell's last command: differs on every call
			b.WriteString(kv + "\x02")
		}
	}
	return b.String()
}

// stateFingerprint describes every file a store-only open of home (from
// repoDir) builds its state from; "" when the home cannot be read (no
// reuse).
func stateFingerprint(home, repoDir string) string {
	var b strings.Builder
	add := func(p string, fi fs.FileInfo) {
		b.WriteString(p + "\x00" + strconv.FormatInt(fi.Size(), 10) + "\x00" + strconv.FormatInt(fi.ModTime().UnixNano(), 10) + "\x00" + fi.Mode().String() + "\x01")
	}
	walk := func(root string) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil // a folder that went away or cannot be read: its files are gone from the print
			}
			if fi, err := d.Info(); err == nil {
				add(p, fi)
			}
			return nil
		})
	}
	entries := func(dir string) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if fi, err := e.Info(); err == nil {
				add(filepath.Join(dir, e.Name()), fi)
			}
		}
	}
	fi, err := os.Stat(home)
	if err != nil || !fi.IsDir() {
		return ""
	}
	if fi, err := os.Stat(filepath.Join(home, "config.yaml")); err == nil {
		add("config.yaml", fi)
	}
	entries(filepath.Join(home, "state"))
	walk(filepath.Join(home, "cache"))
	var clones []string
	for _, pat := range []string{"*", "*/*", "*/*/*"} {
		hits, _ := filepath.Glob(filepath.Join(home, "stores", pat, ".git"))
		clones = append(clones, hits...)
	}
	slices.Sort(clones)
	for _, g := range clones {
		for _, f := range []string{"HEAD", "index", "packed-refs", "FETCH_HEAD", "ORIG_HEAD"} {
			if fi, err := os.Stat(filepath.Join(g, f)); err == nil {
				add(filepath.Join(g, f), fi)
			}
		}
		walk(filepath.Join(g, "refs"))
	}
	if repoDir != "" {
		entries(filepath.Join(repoDir, ".lore"))
	}
	return b.String()
}

// takeState returns the idle state of key when it is under stateMaxAge
// and the files it was built from are unchanged; nil otherwise (the caller
// opens).
func takeState(key, home, repoDir string) *state {
	statePool.mu.Lock()
	p := statePool.idle[key]
	delete(statePool.idle, key)
	statePool.mu.Unlock()
	if p == nil {
		stateStats.misses.Add(1)
		return nil
	}
	if fp := stateFingerprint(home, repoDir); fp == "" || fp != p.fp || time.Since(p.at) > stateMaxAge {
		_ = p.st.close()
		stateStats.stale.Add(1)
		return nil
	}
	stateStats.hits.Add(1)
	return p.st
}

// putState keeps st idle for the next call with its key; false when it
// must close: reuse off, not a kept open, or it wrote (a write's state
// carries what it wrote; the next call opens afresh).
func putState(st *state, home, repoDir string) bool {
	if st.poolKey == "" || !stateReuseOn() || len(st.wroteTo) > 0 {
		return false
	}
	for _, f := range st.storeFormat {
		if f.written {
			return false
		}
	}
	fp := stateFingerprint(home, repoDir)
	if fp == "" {
		return false
	}
	var evict []*state
	statePool.mu.Lock()
	if !statePool.on {
		statePool.mu.Unlock()
		return false
	}
	if old := statePool.idle[st.poolKey]; old != nil && old.st != st {
		evict = append(evict, old.st) // one kept open per key: the newest wins
	}
	statePool.idle[st.poolKey] = &idleState{st: st, fp: fp, at: st.openedAt}
	for k, v := range statePool.idle {
		if len(statePool.idle) <= stateIdleMax {
			break
		}
		if k != st.poolKey {
			evict = append(evict, v.st)
			delete(statePool.idle, k)
		}
	}
	statePool.mu.Unlock()
	for _, e := range evict {
		_ = e.close()
	}
	return true
}
