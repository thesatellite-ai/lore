package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// testRules are the config defaults (cache.max_age 14d, cache.max_size 2 GiB).
var testRules = Rules{MaxAge: orgconf.DefaultCacheMaxAge, MaxSize: orgconf.DefaultCacheMaxSize}

// sweepHome is a lore home's cache root with one live repo checkout.
type sweepHome struct {
	root, repo string
}

func newSweepHome(t *testing.T) sweepHome {
	t.Helper()
	home := t.TempDir()
	h := sweepHome{root: filepath.Join(home, CacheDir), repo: filepath.Join(home, "code", "demo-app")}
	if err := os.MkdirAll(filepath.Join(h.repo, ".lore"), 0o700); err != nil {
		t.Fatal(err)
	}
	return h
}

// cacheFile writes a cache file of n bytes at rel and dates it at.
func (h sweepHome) cacheFile(t *testing.T, rel string, n int, at time.Time) string {
	t.Helper()
	p := filepath.Join(h.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	return rel
}

// record writes repo's use record listing rels and dates it at.
func (h sweepHome) record(t *testing.T, repo string, at time.Time, rels ...string) {
	t.Helper()
	uses := make([]sqlite.Use, 0, len(rels))
	for _, r := range rels {
		uses = append(uses, sqlite.Use{Path: r, Store: "acme"})
	}
	if err := sqlite.RecordUse(h.root, repo, uses); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sqlite.UseRecordPath(h.root, repo), at, at); err != nil {
		t.Fatal(err)
	}
}

func onDisk(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// staleSet is the sweep's stale caches by Rel with their reason.
func staleSet(sw Sweep) map[string]Reason {
	out := map[string]Reason{}
	for _, s := range sw.Stale {
		out[s.Rel] = s.Reason
	}
	return out
}

// TestSweepGoneCopyAndMaxAge pins public issue #178: the repo cache of a
// copy that was deleted is stale; a live repo's cache unused for longer
// than cache.max_age is stale; a live repo's cache used today is kept,
// and so is a gone copy's cache made within the grace (its command may
// still be running).
func TestSweepGoneCopyAndMaxAge(t *testing.T) {
	h := newSweepHome(t)
	now := time.Now()
	old := now.Add(-20 * orgconf.Day)
	liveRepo := h.cacheFile(t, sqlite.RepoCacheRel(h.repo), 10, now)
	h.record(t, h.repo, now, "stores/sto_a/main/acme.db")
	h.cacheFile(t, "stores/sto_a/main/acme.db", 10, old) // written long ago, read today: kept
	gone := filepath.Join(filepath.Dir(h.repo), "copy-1")
	goneRel := h.cacheFile(t, sqlite.RepoCacheRel(gone), 10, now.Add(-2*time.Hour))
	freshGone := filepath.Join(filepath.Dir(h.repo), "copy-2")
	freshRel := h.cacheFile(t, sqlite.RepoCacheRel(freshGone), 10, now.Add(-time.Minute))
	idle := filepath.Join(filepath.Dir(h.repo), "idle-app")
	if err := os.MkdirAll(filepath.Join(idle, ".lore"), 0o700); err != nil {
		t.Fatal(err)
	}
	idleRel := h.cacheFile(t, sqlite.RepoCacheRel(idle), 10, old)
	h.record(t, idle, old)

	sw, err := MakeSweep(h.root, Rules{MaxAge: testRules.MaxAge, MaxSize: testRules.MaxSize, Grace: AutoGrace}, now)
	if err != nil {
		t.Fatal(err)
	}
	got := staleSet(sw)
	if got[goneRel] != ReasonUnattached {
		t.Errorf("gone copy's cache %s: reason %q, want unattached (all: %v)", goneRel, got[goneRel], got)
	}
	if got[idleRel] != ReasonUnused {
		t.Errorf("cache unused for 20 days %s: reason %q, want unused (all: %v)", idleRel, got[idleRel], got)
	}
	for _, kept := range []string{liveRepo, "stores/sto_a/main/acme.db", freshRel} {
		if _, ok := got[kept]; ok {
			t.Errorf("%s is in use (or just made) but the sweep removes it (%v)", kept, got)
		}
	}
	if sw.Files != 5 {
		t.Errorf("Files = %d, want 5", sw.Files)
	}
}

// TestSweepLRUCap: past cache.max_size the least recently used caches go
// first, and never one used within the last day.
func TestSweepLRUCap(t *testing.T) {
	h := newSweepHome(t)
	now := time.Now()
	var rels []string
	for i, age := range []time.Duration{0, 2 * orgconf.Day, 3 * orgconf.Day, 4 * orgconf.Day} {
		rel := h.cacheFile(t, "combined/view-"+string(rune('a'+i))+".db", 100, now.Add(-age))
		rels = append(rels, rel)
	}
	h.record(t, h.repo, now.Add(-5*orgconf.Day), rels...)
	sw, err := MakeSweep(h.root, Rules{MaxAge: testRules.MaxAge, MaxSize: 250, Grace: AutoGrace}, now)
	if err != nil {
		t.Fatal(err)
	}
	got := staleSet(sw)
	if _, ok := got[rels[0]]; ok || got[rels[1]] == ReasonOverSize {
		t.Errorf("the two newest views must stay under a 250-byte cap: %v", got)
	}
	if got[rels[2]] != ReasonOverSize || got[rels[3]] != ReasonOverSize {
		t.Errorf("the two oldest views must go past the cap: %v", got)
	}
	// Used today, all of them: the cap removes nothing.
	for _, r := range rels {
		h.cacheFile(t, r, 100, now)
	}
	if sw, err = MakeSweep(h.root, Rules{MaxAge: testRules.MaxAge, MaxSize: 250, Grace: AutoGrace}, now); err != nil {
		t.Fatal(err)
	}
	if len(sw.Stale) != 0 {
		t.Errorf("views used within a day went for the cap: %v", staleSet(sw))
	}
}

// TestAutoPruneDailyAndLocked: the automatic pass removes a gone copy's
// cache, runs at most once a day, and skips while another lore holds the
// lock.
func TestAutoPruneDailyAndLocked(t *testing.T) {
	h := newSweepHome(t)
	now := time.Now()
	gone := filepath.Join(filepath.Dir(h.repo), "copy-1")
	rel := h.cacheFile(t, sqlite.RepoCacheRel(gone), 10, now.Add(-2*time.Hour))
	h.record(t, gone, now.Add(-2*time.Hour))
	path := filepath.Join(h.root, filepath.FromSlash(rel))

	// Another lore holds a fresh lock: nothing happens, nothing stamped.
	lock := filepath.Join(h.root, AutoLockFile)
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := AutoPrune(context.Background(), h.root, testRules, now)
	if err != nil || out.Ran || !onDisk(path) || onDisk(filepath.Join(h.root, AutoStampFile)) {
		t.Fatalf("locked pass: out=%+v err=%v cache kept=%v", out, err, onDisk(path))
	}
	// A lock left by a lore killed long ago is taken over.
	if err := os.Chtimes(lock, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	out, err = AutoPrune(context.Background(), h.root, testRules, now)
	if err != nil || !out.Ran || out.Removed != 1 || out.Records != 1 || onDisk(path) {
		t.Fatalf("first pass: out=%+v err=%v cache still there=%v", out, err, onDisk(path))
	}
	if onDisk(lock) {
		t.Error("the pass left its lock behind")
	}
	// Within the day: not due, even with a new stale cache.
	h.cacheFile(t, rel, 10, now.Add(-2*time.Hour))
	if out, _ = AutoPrune(context.Background(), h.root, testRules, now.Add(time.Hour)); out.Ran || !onDisk(path) {
		t.Fatalf("second pass within a day ran: %+v", out)
	}
	// A day later: due again.
	if out, _ = AutoPrune(context.Background(), h.root, testRules, now.Add(AutoEvery+time.Minute)); !out.Ran || onDisk(path) {
		t.Fatalf("pass a day later did not run: %+v", out)
	}
}

// TestRecordUseTouchesUnchangedRecord: an open whose uses did not change
// still moves its record's time (at most hourly), so a cache read every
// day is never "unused".
func TestRecordUseTouchesUnchangedRecord(t *testing.T) {
	h := newSweepHome(t)
	old := time.Now().Add(-20 * orgconf.Day)
	h.record(t, h.repo, old, "stores/sto_a/main/acme.db")
	if err := sqlite.RecordUse(h.root, h.repo, []sqlite.Use{{Path: "stores/sto_a/main/acme.db", Store: "acme"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(sqlite.UseRecordPath(h.root, h.repo))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(fi.ModTime()) > time.Minute {
		t.Errorf("record time %v was not moved to now", fi.ModTime())
	}
}
