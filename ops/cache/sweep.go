package cache

// sweep.go — the cache cleans itself (public issue #178). lore keeps one
// cache per repo checkout and per attached store, and before this nothing
// removed them unless a person ran `lore cache prune`: a machine where
// agents make short-lived worktrees and clones reached 6.5 GB of caches of
// copies long deleted.
//
//	MakeSweep(root, rules, now)
//	  ├─ MakePlan: caches no live repo attaches (gone, moved or detached)
//	  │            ─▶ stale once unused for rules.Grace (none for a person's prune)
//	  ├─ every other cache unused for rules.MaxAge (cache.max_age, 14d) ─▶ stale
//	  └─ the rest, newest use first, past rules.MaxSize (cache.max_size, 2 GiB)
//	     ─▶ stale, except a cache used within LRUKeep (a day)
//
//	AutoPrune (any lore command, after its output)
//	  ├─ <cache>/.auto-prune younger than a day ─▶ nothing (one stat: the hot path)
//	  ├─ <cache>/.auto-prune.lock held by another lore ─▶ nothing
//	  └─ stamp, sweep with AutoGrace, remove within autoBudget, drop dead records
//
// "Used" is the newest of: the cache file's own write times (file, WAL,
// stamp) and the time of each live repo's use record that lists it (a
// core open moves it at most once an hour, sqlite.UseTouchEvery). A cache
// in use therefore has a recent time and is never stale: a command opens
// its caches and records them within the same second, and the read
// server's calls are core opens too. Every cache is derived data: a repo
// that needs a removed one rebuilds it from the row files on its next
// command.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Timing of the automatic pass.
const (
	// AutoEvery is how often the automatic pass runs: at most once a day.
	AutoEvery = 24 * time.Hour
	// AutoGrace keeps an unattached cache written or used within the last
	// hour: a command that just made it may not have written its use
	// record yet.
	AutoGrace = time.Hour
	// LRUKeep: cache.max_size never removes a cache used within the last
	// day; a machine whose live set is bigger than the cap keeps it.
	LRUKeep = 24 * time.Hour
	// autoBudget caps how long one automatic pass may delete; the next
	// pass finishes the rest.
	autoBudget = 5 * time.Second
	// autoLockStale is how old a lock may get before the next pass takes
	// it over (a lore killed mid-pass).
	autoLockStale = 10 * time.Minute
	// AutoStampFile and AutoLockFile sit in the cache root.
	AutoStampFile = ".auto-prune"
	AutoLockFile  = ".auto-prune.lock"
	autoFileMode  = 0o600
)

// Reason says why the sweep removes a cache.
type Reason string

// Reasons of a stale cache.
const (
	// ReasonUnattached: no repo on this machine attaches it any more.
	ReasonUnattached Reason = "unattached"
	// ReasonUnused: unused for longer than cache.max_age.
	ReasonUnused Reason = "unused"
	// ReasonOverSize: past cache.max_size, least recently used first.
	ReasonOverSize Reason = "over_size"
)

// staleKind is how a stale entry is removed.
type staleKind int

const (
	kindCache     staleKind = iota // stores/, combined/, repo/: sqlite.RemoveCache
	kindV017Cache                  // a 0.1.17 file in the root: removeV017Cache
	kindV017Tree                   // a 0.1.17 store cache tree: removeV017Tree
)

// Rules are the limits a sweep applies.
type Rules struct {
	// MaxAge is cache.max_age.
	MaxAge time.Duration
	// MaxSize is cache.max_size in bytes.
	MaxSize int64
	// Grace keeps an unattached cache used more recently than this.
	Grace time.Duration
	// ByHand is a person's `lore cache prune`: no grace, and 0.1.17's
	// unattached caches go at once (the automatic pass waits max_age for
	// them, since no record ever says an older lore still reads them).
	ByHand bool
}

// Stale is one cache the sweep removes.
type Stale struct {
	sqlite.File
	Reason Reason
	// Used is the cache's last use (see the file comment).
	Used time.Time
	kind staleKind
}

// Sweep is what one sweep found under a cache root.
type Sweep struct {
	// Stale are the caches to remove, unattached ones first.
	Stale []Stale
	// Dead are use records of repos gone from this machine.
	Dead []sqlite.RecordFile
	// Files and Bytes are every cache the root holds, stale ones included.
	Files int
	Bytes int64
	// StaleBytes is the size of Stale.
	StaleBytes int64
}

// MakeSweep decides what the cache sweep removes under root (see the file
// comment). It only reads.
func MakeSweep(root string, r Rules, now time.Time) (Sweep, error) {
	var sw Sweep
	plan, err := MakePlan(root)
	if err != nil {
		return sw, err
	}
	sw.Dead = plan.Dead
	unattached := map[string]bool{}
	for _, f := range plan.Stores {
		unattached[f.Rel] = true
	}
	for _, f := range plan.Repos {
		unattached[f.Rel] = true
	}
	stores, err := sqlite.StoreCaches(root)
	if err != nil {
		return sw, err
	}
	repos, err := sqlite.RepoCaches(root)
	if err != nil {
		return sw, err
	}
	add := func(s Stale) {
		sw.Stale = append(sw.Stale, s)
		sw.StaleBytes += s.Size
	}
	var live []Stale
	for _, f := range append(stores, repos...) {
		if inTree(plan.V017Trees, f.Rel) {
			continue // counted with its tree below
		}
		sw.Files++
		sw.Bytes += f.Size
		used := sqlite.LastWrite(root, f.Rel)
		if u := plan.Used[f.Rel]; u.After(used) {
			used = u
		}
		age := now.Sub(used)
		switch {
		case unattached[f.Rel] && age >= r.Grace:
			add(Stale{File: f, Reason: ReasonUnattached, Used: used})
		case unattached[f.Rel]:
			// just made: its record may not be written yet
		case r.MaxAge > 0 && age >= r.MaxAge:
			add(Stale{File: f, Reason: ReasonUnused, Used: used})
		default:
			live = append(live, Stale{File: f, Reason: ReasonOverSize, Used: used})
		}
	}
	// 0.1.17's caches: no record ever names them (that lore wrote none),
	// so the automatic pass removes an unattached one only once it is also
	// older than max_age; a person's prune removes it at once.
	v017Wait := max(r.Grace, r.MaxAge)
	if r.ByHand {
		v017Wait = 0
	}
	unattachedV017 := map[string]bool{}
	for _, f := range plan.V017 {
		unattachedV017[f.Rel] = true
	}
	old, err := v017Caches(root)
	if err != nil {
		return sw, err
	}
	for _, f := range old {
		sw.Files++
		sw.Bytes += f.Size
		used := modTime(filepath.Join(root, f.Rel))
		if unattachedV017[f.Rel] && now.Sub(used) >= v017Wait {
			add(Stale{File: f, Reason: ReasonUnattached, Used: used, kind: kindV017Cache})
		}
	}
	for _, f := range plan.V017Trees {
		sw.Files++
		sw.Bytes += f.Size
		used := modTime(filepath.Join(root, filepath.FromSlash(f.Rel)))
		if now.Sub(used) >= v017Wait {
			add(Stale{File: f, Reason: ReasonUnattached, Used: used, kind: kindV017Tree})
		}
	}
	if r.MaxSize > 0 {
		sort.SliceStable(live, func(i, j int) bool { return live[i].Used.After(live[j].Used) })
		var kept int64
		for _, s := range live {
			if kept+s.Size > r.MaxSize && now.Sub(s.Used) >= LRUKeep {
				add(s)
				continue
			}
			kept += s.Size
		}
	}
	return sw, nil
}

// modTime is p's modification time, the zero time when it cannot be read.
func modTime(p string) time.Time {
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// remove deletes one stale entry.
func (s Stale) remove(root string) error {
	switch s.kind {
	case kindV017Cache:
		return removeV017Cache(root, s.Rel)
	case kindV017Tree:
		return removeV017Tree(root, s.Rel)
	default:
		return sqlite.RemoveCache(root, s.Rel)
	}
}

// AutoOut is what one automatic pass did.
type AutoOut struct {
	// Ran is false when the pass was not due, another lore held the lock,
	// or there is no cache folder.
	Ran bool
	// Removed and Bytes count the caches removed; Records the use records
	// of gone repos dropped.
	Removed int
	Bytes   int64
	Records int
}

// AutoPrune is the automatic pass (see the file comment): silent, at most
// once a day, one pass at a time across processes, at most autoBudget of
// deleting. The errors it returns are for tests and logs; callers ignore
// them, a command never fails because of its cache.
func AutoPrune(ctx context.Context, root string, r Rules, now time.Time) (AutoOut, error) {
	var out AutoOut
	stamp := filepath.Join(root, AutoStampFile)
	if fi, err := os.Stat(stamp); err == nil && now.Sub(fi.ModTime()) < AutoEvery {
		return out, nil
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return out, nil // no cache yet: nothing to sweep
	}
	release, ok := lockAuto(filepath.Join(root, AutoLockFile), now)
	if !ok {
		return out, nil // another lore is sweeping
	}
	defer release()
	// Stamped first: a pass that fails is not retried by every command.
	if err := os.WriteFile(stamp, nil, autoFileMode); err != nil {
		return out, errs.FileIO("stamp the cache sweep", stamp, err)
	}
	_ = os.Chtimes(stamp, now, now) // the clock the next pass compares with
	out.Ran = true
	r.Grace = max(r.Grace, AutoGrace)
	sw, err := MakeSweep(root, r, now)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, autoBudget)
	defer cancel()
	var all []error
	for _, s := range sw.Stale {
		if ctx.Err() != nil {
			break // the rest goes in the next pass
		}
		if rerr := s.remove(root); rerr != nil {
			all = append(all, rerr)
			continue
		}
		out.Removed++
		out.Bytes += s.Size
	}
	n, derr := dropDead(root, sw.Dead)
	out.Records = n
	return out, errors.Join(append(all, derr)...)
}

// lockAuto takes the sweep's lock file (created exclusively); a lock older
// than autoLockStale is a lore killed mid-pass and is taken over once.
func lockAuto(path string, now time.Time) (release func(), ok bool) {
	for try := 0; try < 2; try++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, autoFileMode)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, true
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, false
		}
		fi, serr := os.Stat(path)
		if serr != nil || now.Sub(fi.ModTime()) < autoLockStale {
			return nil, false
		}
		_ = os.Remove(path) // stale: its lore is gone
	}
	return nil, false
}
