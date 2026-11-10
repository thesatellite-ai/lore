package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"

	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// gitIn runs git in dir with a fixed identity and no hooks.
func metaGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	all := append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)
	if _, err := liveGit(context.Background(), dir, all...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

// keyRowFile finds the one meta_keys row file under repo.
func keyRowFile(t *testing.T, repo string) (abs, id string) {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(repo, ".lore", "data", string(schema.TableMetaKeys), "*.json"))
	if err != nil || len(m) != 1 {
		t.Fatalf("key row files %v %v", m, err)
	}
	return m[0], strings.TrimSuffix(filepath.Base(m[0]), ".json")
}

// TestMetaRebuildE2E runs `lore meta rebuild` against a real git checkout
// (OV64, METADATA 3.18): a key row deleted only from the work tree is "not
// lost" (in HEAD, E_REFUSED naming it); a damaged work-tree copy with no
// readable copy anywhere is "damaged", refused; a row deleted and committed
// is rebuilt from history with its id, per-ref absent counts reported.
func TestMetaRebuildE2E(t *testing.T) {
	// Heavy on SQLite: its own process keeps it off this process's
	// allocator lock (see package isolate).
	if isolate.Run(t) {
		return
	}
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	if _, err := run(t, a, settings.OpMetaAdd, `{"name":"client","type":"text"}`, true); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, a, opsmemory.OpAdd, `{"body":"rebuild probe"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var mem struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal([]byte(out), &mem); err != nil || mem.ID == "" {
		t.Fatalf("memory add: %s %v", out, err)
	}
	if _, err = run(t, a, settings.OpMetaSet, `{"id":"`+mem.ID+`","pair":"client=acme"}`, true); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	metaGit(t, repo, "add", "-A")
	metaGit(t, repo, "commit", "-q", "-m", "one")
	path, keyID := keyRowFile(t, repo)

	rebuild := func(input string) (settings.MetaRebuildOut, error) {
		t.Helper()
		b := openWith(t, repo, Options{})
		s, rerr := run(t, b, settings.OpMetaRebuild, input, true)
		if rerr != nil {
			return settings.MetaRebuildOut{}, rerr
		}
		var o settings.MetaRebuildOut
		if jerr := json.Unmarshal([]byte(s), &o); jerr != nil {
			t.Fatal(jerr)
		}
		return o, nil
	}

	// 1. deleted from the work tree only: HEAD has it → not lost
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err = rebuild(`{"dry_run":true}`); !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), "is in HEAD") {
		t.Fatalf("work-tree delete: %v", err)
	}

	// 2. committed delete + a damaged copy in the work tree → damaged
	metaGit(t, repo, "add", "-A")
	metaGit(t, repo, "commit", "-q", "-m", "lose the key row")
	if err = os.WriteFile(path, []byte(`{"_v":2,"broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = rebuild(`{"dry_run":true}`); !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), "damaged in the work tree") {
		t.Fatalf("damaged copy: %v", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}

	// 3. really lost: rebuilt from history, same id, absent on every ref
	o, err := rebuild(`{"dry_run":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Keys) != 1 || o.Keys[0].ID != keyID || o.Keys[0].Name != "client" || o.Keys[0].Source != "history" || o.Written != 0 {
		t.Fatalf("dry run %+v", o)
	}
	if len(o.Refs) == 0 {
		t.Fatal("no refs reported")
	}
	for _, r := range o.Refs {
		if r.Absent != 1 {
			t.Errorf("ref %s absent %d, want 1", r.Ref, r.Absent)
		}
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Fatal("dry run wrote the row")
	}
	if o, err = rebuild(`{}`); err != nil || o.Written != 1 {
		t.Fatalf("rebuild %+v %v", o, err)
	}
	back, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("row not recreated at its id: %v", err)
	}
	if !strings.Contains(string(back), `"client"`) {
		t.Errorf("rebuilt row %s", back)
	}
	// 4. nothing missing now
	if o, err = rebuild(`{"dry_run":true}`); err != nil || len(o.Keys) != 0 {
		t.Fatalf("after rebuild %+v %v", o, err)
	}
}

// TestMetaRebuildFindsOtherBranch: a key row lost on the checked-out
// branch but present on another local branch is "not lost" and the refusal
// names that branch (3.18 step 2: every local and fetched ref).
func TestMetaRebuildFindsOtherBranch(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	if _, err := run(t, a, settings.OpMetaAdd, `{"name":"client","type":"text"}`, true); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, a, opsmemory.OpAdd, `{"body":"x"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var mem struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal([]byte(out), &mem); err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, a, settings.OpMetaSet, `{"id":"`+mem.ID+`","pair":"client=acme"}`, true); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	metaGit(t, repo, "add", "-A")
	metaGit(t, repo, "commit", "-q", "-m", "one")
	metaGit(t, repo, "branch", "keep-me")
	path, _ := keyRowFile(t, repo)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	metaGit(t, repo, "add", "-A")
	metaGit(t, repo, "commit", "-q", "-m", "lose it here")
	b := openWith(t, repo, Options{})
	_, err = run(t, b, settings.OpMetaRebuild, `{"dry_run":true}`, true)
	if !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), "is in keep-me") {
		t.Fatalf("other branch: %v", err)
	}
}

// TestHoldStoreBlocksSecondHolder: while meta rebuild holds the repo
// store's lock (OV64-HOLD), a second holder (another process's sync) waits
// and gives up at its deadline; after release it gets the lock.
func TestHoldStoreBlocksSecondHolder(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	b := openWith(t, repo, Options{})
	withProjectNoop(t, b)
	_, release, err := a.HoldStore(background(), store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, rel2, herr := b.HoldStore(ctx, store.SourceRepo); herr == nil {
		_ = rel2()
		t.Fatal("second holder got the lock while it was held")
	}
	if time.Since(start) < 200*time.Millisecond {
		t.Errorf("second holder gave up at once (%v), want it to wait", time.Since(start))
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	_, rel3, err := b.HoldStore(background(), store.SourceRepo)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	if err = rel3(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.HoldStore(background(), "nosuch"); !errs.Has(err, errs.NotFound) {
		t.Errorf("unknown store: %v", err)
	}
}

// withProjectNoop opens b so its stores are attached (the project row is
// already there from the first App).
func withProjectNoop(t *testing.T, b *App) {
	t.Helper()
	if _, err := b.Engine(background()); err != nil {
		t.Fatal(err)
	}
}

// TestKeyLooseningsMatrix runs KeyLoosenings over a real git index and
// commit range (RX7, OV64): several key files in one change, an added key
// (not a loosening), a removed key (a tombstone commit, not one), a
// sensitive drop, a label-only edit, a key in a monorepo subfolder, an
// unstaged work-tree edit (not seen by the index check), and a meta_values
// file (not a key row).
func TestKeyLooseningsMatrix(t *testing.T) {
	t.Parallel()
	ctx := background()
	dir := t.TempDir()
	write := func(rel, s string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const (
		kInternal = ".lore/data/meta_keys/mky_a.json"
		kSens     = ".lore/data/meta_keys/mky_b.json"
		kLabel    = ".lore/data/meta_keys/mky_c.json"
		kGone     = ".lore/data/meta_keys/mky_d.json"
		kSub      = "apps/web/.lore/data/meta_keys/mky_e.json"
		kUnstaged = ".lore/data/meta_keys/mky_f.json"
		vRow      = ".lore/data/meta_values/val_a.json"
		kNew      = ".lore/data/meta_keys/mky_n.json"
	)
	metaGit(t, dir, "init", "-q", "-b", "main")
	write(kInternal, `{"name":"margin_pct","internal":true}`)
	write(kSens, `{"name":"vpn_host","sensitive":true}`)
	write(kLabel, `{"name":"client","description":"old"}`)
	write(kGone, `{"name":"gone","internal":true}`)
	write(kSub, `{"name":"rate","internal":true}`)
	write(kUnstaged, `{"name":"cost","internal":true}`)
	write(vRow, `{"name":"x","internal":true}`)
	metaGit(t, dir, "add", "-A")
	metaGit(t, dir, "commit", "-q", "-m", "one")

	write(kInternal, `{"name":"margin_pct","internal":false}`)
	write(kSens, `{"name":"vpn_host"}`)
	write(kLabel, `{"name":"client","description":"new"}`)
	write(kSub, `{"name":"rate"}`)
	write(vRow, `{"name":"x"}`)
	write(kNew, `{"name":"fresh"}`)
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(kGone))); err != nil {
		t.Fatal(err)
	}
	metaGit(t, dir, "add", "-A")
	write(kUnstaged, `{"name":"cost"}`) // after add: not in the index

	found, err := KeyLoosenings(ctx, dir, "HEAD", "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range found {
		if !errs.Has(f, errs.LeakBlocked) {
			t.Errorf("finding is not E_LEAK_BLOCKED: %v", f)
		}
		for _, name := range []string{"mky_a", "mky_b", "mky_c", "mky_d", "mky_e", "mky_f", "mky_n", "val_a"} {
			if strings.Contains(f.Error(), name) {
				got[name] = true
			}
		}
	}
	want := map[string]bool{"mky_a": true, "mky_b": true, "mky_e": true}
	if len(found) != len(want) {
		t.Errorf("found %d: %v", len(found), found)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%s not blocked (found %v)", k, found)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("%s blocked, want not", k)
		}
	}
	// the same change committed: base..HEAD agrees from the top; from a
	// monorepo project subfolder the check covers that project's keys
	// only (the pathspec is relative to dir; each project checks its own)
	metaGit(t, dir, "commit", "-q", "-m", "two")
	since, err := KeyLooseningsSince(ctx, dir, "HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != len(want) {
		t.Errorf("since from the top: %v", since)
	}
	sub, err := KeyLooseningsSince(ctx, filepath.Join(dir, "apps", "web"), "HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 1 || !strings.Contains(sub[0].Error(), "mky_e") {
		t.Errorf("since from a subfolder: %v", sub)
	}
	// a first commit has nothing to compare with
	fresh := t.TempDir()
	metaGit(t, fresh, "init", "-q", "-b", "main")
	if f, ferr := KeyLooseningsSince(ctx, fresh, ""); ferr != nil || len(f) != 0 {
		t.Errorf("no base: %v %v", f, ferr)
	}
}
