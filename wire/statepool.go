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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
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
	var p filePrint
	if !p.home(home) {
		return ""
	}
	if repoDir != "" {
		p.entries(filepath.Join(repoDir, ".lore"))
	}
	return p.b.String()
}

// filePrint builds a fingerprint: the path, size, time and mode of each
// file added.
type filePrint struct {
	b strings.Builder
	// hashNewer, when set, adds a hash of the content of every file walked
	// with walkTree whose time is after it (racyHashWindow).
	hashNewer time.Time
}

func (p *filePrint) add(path string, fi fs.FileInfo) {
	p.b.WriteString(path + "\x00" + strconv.FormatInt(fi.Size(), 10) + "\x00" + strconv.FormatInt(fi.ModTime().UnixNano(), 10) + "\x00" + fi.Mode().String() + "\x01")
}

// walk adds every file under root (a folder that went away or cannot be
// read: its files are gone from the print).
func (p *filePrint) walk(root string) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			p.add(path, fi)
		}
		return nil
	})
}

// walkTree adds every file of a work tree under root but its .git
// folders, with the content hash of a file newer than hashNewer: a hand
// edit lands there, and an edit inside the file system's time step that
// keeps the size changes no stat field (git's "racy" case).
func (p *filePrint) walkTree(root string) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil
		case d.IsDir() && d.Name() == ".git":
			return filepath.SkipDir
		case d.IsDir():
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		p.add(path, fi)
		if fi.Mode().IsRegular() && !fi.ModTime().Before(p.hashNewer) {
			if body, err := os.ReadFile(path); err == nil {
				sum := sha256.Sum256(body)
				p.b.WriteString(hex.EncodeToString(sum[:]) + "\x01")
			}
		}
		return nil
	})
}

// entries adds each entry of dir, not what is below it.
func (p *filePrint) entries(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if fi, err := e.Info(); err == nil {
			p.add(filepath.Join(dir, e.Name()), fi)
		}
	}
}

// home adds what every open builds from under the lore home (see the top
// of this file); false when home is not a readable folder.
func (p *filePrint) home(home string) bool {
	fi, err := os.Stat(home)
	if err != nil || !fi.IsDir() {
		return false
	}
	if fi, err := os.Stat(filepath.Join(home, "config.yaml")); err == nil {
		p.add("config.yaml", fi)
	}
	p.entries(filepath.Join(home, "state"))
	p.walk(filepath.Join(home, "cache"))
	for _, g := range storeGitDirs(home) {
		p.gitDir(g)
	}
	return true
}

// gitDir adds the files of a git folder a commit, a checkout, a fetch or
// a sync moves.
func (p *filePrint) gitDir(g string) {
	for _, f := range []string{"HEAD", "index", "packed-refs", "FETCH_HEAD", "ORIG_HEAD"} {
		if fi, err := os.Stat(filepath.Join(g, f)); err == nil {
			p.add(filepath.Join(g, f), fi)
		}
	}
	p.walk(filepath.Join(g, "refs"))
}

// storeGitDirs is the .git folder of every store clone under home.
func storeGitDirs(home string) []string {
	var clones []string
	for _, pat := range []string{"*", "*/*", "*/*/*"} {
		hits, _ := filepath.Glob(filepath.Join(home, "stores", pat, ".git"))
		clones = append(clones, hits...)
	}
	slices.Sort(clones)
	return clones
}

// racyHashWindow is how recent a work tree file must be, before the open
// that printed it, for the fingerprint of a kept repo open to hash its
// content (filePrint.walkTree): well past any file system's time step, as
// the git status stamp's racy guard (adapters/backend/git statusstamp.go).
const racyHashWindow = 2 * time.Second

// treeHashSince is the cutoff of a work tree print taken now: files from
// racyHashWindow before it on are hashed. A kept open keeps the cutoff of
// its print (state.treeSince) and is checked against a print with that
// same cutoff, never a later one (bug hunt H-217, devco:D-958): with the
// cutoff moved to each check's own now, a file written in the 2 s before
// the open lost its hash once it aged past the line, so the print of an
// unchanged tree differed and every read in those seconds opened the repo
// and its stores afresh and ran git status in each clone (~100 ms more).
func treeHashSince(now time.Time) time.Time { return now.Add(-racyHashWindow) }

// repoStateFingerprint describes every file a full repo open (App.state)
// builds its state from, on top of stateFingerprint's: the files at the
// top of the lore home (machine.json), the repo's git folder (a commit, a
// checkout or a branch switch moves the live rows and the git status the
// pick-up reads) and tree, the work tree print (repoTreePrint). "" when
// one cannot be read.
func repoStateFingerprint(home, repoTop, tree string) string {
	var p filePrint
	if repoTop == "" || tree == "" || !p.home(home) {
		return ""
	}
	ents, err := os.ReadDir(home)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() {
			p.add(filepath.Join(home, e.Name()), fi)
		}
	}
	g, ok := repoGitDir(repoTop)
	if !ok {
		return ""
	}
	p.gitDir(g)
	if common, err := os.ReadFile(filepath.Join(g, "commondir")); err == nil { // a linked worktree: its refs live in the main repo's git folder
		c := strings.TrimSpace(string(common))
		if !filepath.IsAbs(c) {
			c = filepath.Join(g, c)
		}
		p.gitDir(c)
	}
	return p.b.String() + tree
}

// repoTreePrint describes the work trees a person edits by hand: every
// store clone's (a hand edit there is picked up by the open) and every
// file of the repo's .lore folder (its rows, stores.yaml). It is taken
// once per command, before the open reads them (a new open) or when a
// kept open is handed out, and the kept open carries it to the end of the
// command: a read writes none of these files (a pick-up or any row write
// marks the state written, and it is not kept), so an edit made while the
// command ran differs from it and the next command opens afresh. Walking
// them again at the end would cost every command a second walk and take
// in such an edit as seen. since is the hash cutoff (treeHashSince): the
// open's own when it is taken for an open, the kept open's when it is
// checked, so an unchanged tree prints the same both times. "" when top is
// "".
func repoTreePrint(home, top string, since time.Time) string {
	if top == "" {
		return ""
	}
	p := filePrint{hashNewer: since}
	p.walkTree(filepath.Join(home, "stores"))
	p.walkTree(filepath.Join(top, ".lore"))
	return "tree\x01" + p.b.String()
}

// gitTopOf is the nearest folder at or above dir holding .git ("" when
// none): the repo top an open of dir finds.
func gitTopOf(dir string) string {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// repoGitDir is the git folder of the repo at top: top/.git, or the
// folder a linked worktree's .git file names.
func repoGitDir(top string) (string, bool) {
	g := filepath.Join(top, ".git")
	fi, err := os.Stat(g)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return g, true
	}
	body, err := os.ReadFile(g)
	if err != nil {
		return "", false
	}
	d, ok := strings.CutPrefix(strings.TrimSpace(string(body)), "gitdir:")
	if !ok {
		return "", false
	}
	d = strings.TrimSpace(d)
	if !filepath.IsAbs(d) {
		d = filepath.Join(top, d)
	}
	return d, true
}

// fingerprint is the fingerprint of the files st was built from: a kept
// repo open's (repoStateFingerprint) or a store-only open's.
func (st *state) fingerprint(home, repoDir string) string {
	if st.repoKept {
		return repoStateFingerprint(home, st.repoTop, st.treePrint)
	}
	return stateFingerprint(home, repoDir)
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
	fp := ""
	if p.st.repoKept {
		fp = repoStateFingerprint(home, p.st.repoTop, repoTreePrint(home, p.st.repoTop, p.st.treeSince)) // the work trees as they are now, hashed from the kept print's cutoff
	} else {
		fp = p.st.fingerprint(home, repoDir)
	}
	if fp == "" || fp != p.fp || time.Since(p.at) > stateMaxAge {
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
	if st.poolKey == "" || !stateReuseOn() || len(st.wroteTo) > 0 || st.saidOnFinish {
		return false
	}
	for _, f := range st.storeFormat {
		if f.written {
			return false
		}
	}
	fp := st.fingerprint(home, repoDir)
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

// repoStateKey is the read server's key of a full repo open (App.state):
// stateKey's inputs (home, repo dir, the whole environment), every plain
// value of the options (an actor flag, person-only), whether the command
// is a read run (doctor_nocreate.go: such a run opens a repo with no lore
// project read-only) and the identity the caller resolves to, with what
// agent detection saw (it walks the caller's parent processes, which the
// environment does not name). "" when the identity does not resolve: no
// reuse.
func repoStateKey(ctx context.Context, opts Options) string {
	who, err := resolveIdentity(ctx, opts)
	if err != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("repo-open\x01" + stateKey(opts, attach.Store{}, "", nil))
	b.WriteString("\x01opts\x00")
	plainPrint(&b, reflect.ValueOf(opts), 0)
	b.WriteString("\x01read\x00" + strconv.FormatBool(noCreateOf(ctx) != nil) + "\x01who\x00")
	plainPrint(&b, reflect.ValueOf(who), 0)
	return b.String()
}

// plainPrintDepth bounds plainPrint (a value refers to itself through no
// plain field, but a deep one costs every call).
const plainPrintDepth = 6

// plainPrint writes the plain data of v (text, numbers, flags, and lists,
// string-keyed maps, structs and pointers of them, unexported fields
// included) to b. Funcs, interfaces and channels are the host's wiring of
// one command and print as "-": they never decide which files an open
// reads or who it opens for, and their addresses differ on every call.
func plainPrint(b *strings.Builder, v reflect.Value, depth int) {
	if depth > plainPrintDepth {
		b.WriteString("…")
		return
	}
	switch v.Kind() {
	case reflect.Bool:
		b.WriteString(strconv.FormatBool(v.Bool()))
	case reflect.String:
		b.WriteString(strconv.Quote(v.String()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		b.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		b.WriteString(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	case reflect.Pointer:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		plainPrint(b, v.Elem(), depth+1)
	case reflect.Slice, reflect.Array:
		b.WriteString("[" + strconv.Itoa(v.Len()) + ":")
		for i := range v.Len() {
			plainPrint(b, v.Index(i), depth+1)
			b.WriteString(",")
		}
		b.WriteString("]")
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			b.WriteString("-")
			return
		}
		keys := v.MapKeys()
		slices.SortFunc(keys, func(x, y reflect.Value) int { return strings.Compare(x.String(), y.String()) })
		b.WriteString("{")
		for _, k := range keys {
			b.WriteString(strconv.Quote(k.String()) + ":")
			plainPrint(b, v.MapIndex(k), depth+1)
			b.WriteString(",")
		}
		b.WriteString("}")
	case reflect.Struct:
		t := v.Type()
		b.WriteString("{")
		for i := range t.NumField() {
			b.WriteString(t.Field(i).Name + ":")
			plainPrint(b, v.Field(i), depth+1)
			b.WriteString(",")
		}
		b.WriteString("}")
	default: // func, interface, chan, unsafe pointer
		b.WriteString("-")
	}
}
