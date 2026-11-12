package cache

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
)

// fixture is a cache root with: a live repo that uses one store cache, a
// committed store it no longer names, its v0.1.17 project cache; a gone
// repo's record and cache; and an orphan v0.1.17 cache.
type fixture struct {
	root, repo             string
	kept, detached, orphan string
	v017Kept, v017Orphan   string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	home := t.TempDir()
	f := fixture{root: filepath.Join(home, CacheDir), repo: filepath.Join(home, "repo")}
	write := func(rel string, n int) string {
		t.Helper()
		p := filepath.Join(f.root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0o600); err != nil {
			t.Fatal(err)
		}
		return rel
	}
	f.kept = write("stores/sto_a/main/acme.db", 10)
	f.detached = write("stores/sto_b/main/old.db", 20)
	f.orphan = write("stores/sto_c/main/gone.db", 30)
	prj, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	other, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	f.v017Kept = write(prj.String()+v017CacheExt, 5)
	f.v017Orphan = write(other.String()+v017CacheExt, 7)
	write("machine-store.db", 3) // the machine store's cache: never a v0.1.17 cache

	// The live repo: stores.yaml names only "acme"; its v0.1.17 marker
	// names prj.
	mustWrite(t, filepath.Join(f.repo, ".lore", "stores.yaml"), "stores:\n  - name: acme\n    url: file:///tmp/acme\n")
	mustWrite(t, filepath.Join(f.repo, filepath.FromSlash(v017MetaRel)), `{"project_id":"`+prj.String()+`"}`)
	if err := sqlite.RecordUse(f.root, f.repo, []sqlite.Use{
		{Path: f.kept, Store: "acme", Committed: true},
		{Path: f.detached, Store: "old", Committed: true},
	}); err != nil {
		t.Fatal(err)
	}
	// A repo that is gone: its record is dead, its cache is an orphan.
	if err := sqlite.RecordUse(f.root, filepath.Join(home, "gone"), []sqlite.Use{{Path: f.orphan, Store: "x"}}); err != nil {
		t.Fatal(err)
	}
	return f
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(t *testing.T, root, rel string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// TestPrunePlanKeepsAttachedCaches: the cache a live repo uses and its
// v0.1.17 project cache stay; a store stores.yaml no longer names, a gone
// repo's cache and an unclaimed v0.1.17 cache are listed (FLAG3).
func TestPrunePlanKeepsAttachedCaches(t *testing.T) {
	f := newFixture(t)
	out, err := Prune(context.Background(), f.root, true, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range out.Files {
		rel, _ := filepath.Rel(f.root, x.Path)
		got = append(got, filepath.ToSlash(rel))
	}
	want := []string{f.detached, f.orphan, f.v017Orphan}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("listed %v, want %v", got, want)
	}
	if out.Bytes != 57 || out.Count != 3 || out.Removed {
		t.Fatalf("out = %+v", out)
	}
	text := out.Text()
	for _, line := range []string{lineHeader, "3 file(s), 57 B", lineDryRun} {
		if !strings.Contains(text, line) {
			t.Fatalf("dry-run text misses %q:\n%s", line, text)
		}
	}
	// A dry run deletes nothing, records included.
	for _, rel := range want {
		if !exists(t, f.root, rel) {
			t.Fatalf("dry run deleted %s", rel)
		}
	}
	if recs, _ := sqlite.ReadUses(f.root); len(recs) != 2 {
		t.Fatalf("dry run dropped records: %d left", len(recs))
	}
}

// TestPruneAsksThenDeletes: unconfirmed is E_NEEDS_CONFIRM naming the
// files and `lore cache prune --yes`, deleting nothing; confirmed deletes
// exactly the listed files and the dead record; a second run has nothing.
func TestPruneAsksThenDeletes(t *testing.T) {
	f := newFixture(t)
	_, err := Prune(context.Background(), f.root, false, false)
	if !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("unconfirmed prune: %v, want E_NEEDS_CONFIRM", err)
	}
	env := errs.EnvelopeOf(err).Error
	if !strings.Contains(env.Message, filepath.FromSlash(f.detached)) || env.Next == nil || strings.Join(env.Next.Args, " ") != "cache prune --yes" {
		t.Fatalf("refusal = %+v", env)
	}
	if !exists(t, f.root, f.orphan) {
		t.Fatal("the refusal deleted a file")
	}
	out, err := Prune(context.Background(), f.root, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Removed || out.Records != 1 || !strings.Contains(out.Text(), "✓ removed 3 file(s), 57 B") {
		t.Fatalf("out = %+v\n%s", out, out.Text())
	}
	for _, rel := range []string{f.detached, f.orphan, f.v017Orphan} {
		if exists(t, f.root, rel) {
			t.Fatalf("%s still there", rel)
		}
	}
	for _, rel := range []string{f.kept, f.v017Kept, "machine-store.db"} {
		if !exists(t, f.root, rel) {
			t.Fatalf("%s was deleted", rel)
		}
	}
	again, err := Prune(context.Background(), f.root, false, false)
	if err != nil || again.Count != 0 || !strings.HasPrefix(again.Text(), lineNothing) {
		t.Fatalf("second run: %+v %v", again, err)
	}
}

// TestPruneUntrustedStoresYAMLKeeps: an unreadable stores.yaml keeps every
// committed cache (fail safe), and a missing cache root is nothing to do.
func TestPruneUntrustedStoresYAMLKeeps(t *testing.T) {
	f := newFixture(t)
	mustWrite(t, filepath.Join(f.repo, ".lore", "stores.yaml"), "stores: [::bad")
	plan, err := MakePlan(f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range plan.Stores {
		if s.Rel == f.detached {
			t.Fatal("an unparsable stores.yaml must keep its committed caches")
		}
	}
	out, err := Prune(context.Background(), filepath.Join(t.TempDir(), "none"), false, false)
	if err != nil || out.Count != 0 {
		t.Fatalf("missing root: %+v %v", out, err)
	}
}

// TestOpsLocalMayAsk: the operation is Local and MayAsk, and reads the
// confirmation from the runner's context (op.WithConfirmed).
func TestOpsLocalMayAsk(t *testing.T) {
	entries := Ops(homeEnv(t.TempDir()))
	if len(entries) != 1 {
		t.Fatalf("%d ops", len(entries))
	}
	s := entries[0].Spec()
	if s.Name != OpPrune || !s.Local || !s.Confirm.MayAsk || s.Confirm.Always || s.Confirm.When != "" {
		t.Fatalf("spec = %+v", s)
	}
	if !op.Confirmed(op.WithConfirmed(context.Background(), true)) || op.Confirmed(context.Background()) {
		t.Fatal("op.Confirmed does not read op.WithConfirmed")
	}
}

// TestHumanBytes pins the size form of the listing.
func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 2202009: "2.1 MB"} {
		if got := HumanBytes(n); got != want {
			t.Fatalf("HumanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

// homeEnv is an Env at a fixed lore home.
type homeEnv string

func (h homeEnv) LoreHome() string { return string(h) }

// TestPruneMovedCheckoutRepoCache (B3-9): after a checkout moves, the repo
// cache named from its old path is listed and removed with the old path's
// record, the new path's cache stays, and the output never says "nothing to
// prune" next to "removed N record(s)".
func TestPruneMovedCheckoutRepoCache(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, CacheDir)
	oldTop, newTop := filepath.Join(home, "w", "website"), filepath.Join(home, "w", "clients", "website")
	mustWrite(t, filepath.Join(newTop, ".lore", "stores.yaml"), "stores: []\n")
	oldCache, newCache := sqlite.RepoCacheRel(oldTop), sqlite.RepoCacheRel(newTop)
	mustWrite(t, filepath.Join(root, filepath.FromSlash(oldCache)), "old")
	mustWrite(t, filepath.Join(root, filepath.FromSlash(newCache)), "new")
	for _, top := range []string{oldTop, newTop} {
		if err := sqlite.RecordUse(root, top, nil); err != nil {
			t.Fatal(err)
		}
	}
	out, err := Prune(context.Background(), root, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Count != 1 || out.Records != 1 || !strings.HasSuffix(filepath.ToSlash(out.Files[0].Path), oldCache) {
		t.Fatalf("out = %+v", out)
	}
	if exists(t, root, oldCache) || !exists(t, root, newCache) {
		t.Fatalf("old cache there: %v, new cache there: %v", exists(t, root, oldCache), exists(t, root, newCache))
	}
	// Only a dead record left to drop: one line, no "nothing to prune".
	if err := sqlite.RecordUse(root, filepath.Join(home, "gone"), nil); err != nil {
		t.Fatal(err)
	}
	only, err := Prune(context.Background(), root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if text := only.Text(); text != "removed 1 cache use record(s) of repos no longer on this machine\n" {
		t.Fatalf("records-only text = %q", text)
	}
}

// TestPruneRemovesV017StoreTree pins B4-7: 0.1.17's store cache tree
// (stores/<host>/…/.lore/…) is listed once as a folder and removed whole,
// json and lock files and empty folders included; a store id folder of
// this lore (no dot) stays.
func TestPruneRemovesV017StoreTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), CacheDir)
	old := filepath.Join(root, "stores", "git.example", "acme", "notes", "main", "web")
	mustWrite(t, filepath.Join(old, ".lore", "external.db"), "db")
	mustWrite(t, filepath.Join(old, ".lore", "data", "rules", "r.json"), "{}")
	mustWrite(t, filepath.Join(old, ".lore", "state", "sync.lock"), "")
	mustWrite(t, filepath.Join(root, "stores", "acme", "main", "web.db"), "db")
	out, err := Prune(context.Background(), root, true, false)
	if err != nil {
		t.Fatal(err)
	}
	var tree int
	for _, f := range out.Files {
		if strings.Contains(f.Path, "git.example") {
			tree++
			if filepath.Base(f.Path) != "git.example" {
				t.Fatalf("the 0.1.17 tree is listed file by file: %s", f.Path)
			}
		}
	}
	if tree != 1 {
		t.Fatalf("0.1.17 tree listed %d times:\n%s", tree, out.Text())
	}
	if _, err := Prune(context.Background(), root, false, true); err != nil {
		t.Fatal(err)
	}
	if exists(t, root, "stores/git.example") {
		t.Fatal("the 0.1.17 tree is still there")
	}
}

// TestPruneMigratedProjectV017Cache pins B4-20: once the live repo of a
// project is migrated (its _meta.json declares min_lore_version) nothing
// reads that project's 0.1.17 cache, so prune lists it too.
func TestPruneMigratedProjectV017Cache(t *testing.T) {
	f := newFixture(t)
	plan, err := MakePlan(f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range plan.V017 {
		if v.Rel == f.v017Kept {
			t.Fatal("an unmigrated project's 0.1.17 cache is listed")
		}
	}
	prj := strings.TrimSuffix(f.v017Kept, v017CacheExt)
	mustWrite(t, filepath.Join(f.repo, filepath.FromSlash(v017MetaRel)), `{"min_lore_version":"0.1.18","project_id":"`+prj+`"}`)
	if plan, err = MakePlan(f.root); err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, v := range plan.V017 {
		listed = listed || v.Rel == f.v017Kept
	}
	if !listed {
		t.Fatalf("a migrated project's 0.1.17 cache is kept: %+v", plan.V017)
	}
}

// TestPruneV017LinkIndex pins B4-20: 0.1.17's links-<org>.json in the
// cache root is listed and removed with its other caches.
func TestPruneV017LinkIndex(t *testing.T) {
	root := filepath.Join(t.TempDir(), CacheDir)
	mustWrite(t, filepath.Join(root, "links-acme.json"), "{}")
	out, err := Prune(context.Background(), root, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Count != 1 || exists(t, root, "links-acme.json") {
		t.Fatalf("out %+v, still there %v", out, exists(t, root, "links-acme.json"))
	}
}
