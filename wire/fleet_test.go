package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestFixRepoWalkCountsReposWithoutLoreData (K-10): the folder walk
// records every git repo with no lore data that no store link names (an
// empty .lore/ included), so fix --all counts them and --verbose names
// them; a known repo outside the folders is not walked, so it is not
// listed, and a repo with lore data is a root, never one of them.
func TestFixRepoWalkCountsReposWithoutLoreData(t *testing.T) {
	base := t.TempDir()
	mk := func(rel string, parts ...string) string {
		p := filepath.Join(base, rel)
		for _, part := range parts {
			if err := os.MkdirAll(filepath.Join(p, part), 0o750); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	withLore := mk("a", ".git", ".lore")
	if err := os.WriteFile(filepath.Join(withLore, ".lore", "LORE.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plain := mk("code/plain", ".git")
	empty := mk("code/emptylore", ".git", ".lore")
	outside := mk("../outside-"+filepath.Base(base), ".git")
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	w := fixRepoWalk{known: []string{outside}, bases: []string{base}}
	got, err := w.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{resolvedPath(empty), resolvedPath(plain)}
	slices.Sort(want)
	if !slices.Equal(got.NoLore, want) {
		t.Fatalf("repos without lore data %v, want %v", got.NoLore, want)
	}
	if !slices.Equal(got.Roots, []string{resolvedPath(withLore)}) {
		t.Fatalf("roots %v", got.Roots)
	}
}

// gitFleetT runs git in dir with a fixed identity and no user config.
func gitFleetT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Ana Test", "GIT_AUTHOR_EMAIL=ana@example.test", "GIT_COMMITTER_NAME=Ana Test", "GIT_COMMITTER_EMAIL=ana@example.test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writeMeta writes .lore/data/_meta.json in the checkout at dir.
func writeMeta(t *testing.T, dir, doc string) {
	t.Helper()
	p := filepath.Join(dir, ".lore", "data", "_meta.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestUpstreamMigratedTellsAStaleCheckoutToPull (K-11): a checkout on the
// lore 0.1.17 format whose fetched origin/main is already on format 2 is
// told to git pull (who migrated, when), and migrate run refuses there;
// before the fetch, or once pulled, nothing is said.
func TestUpstreamMigratedTellsAStaleCheckoutToPull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitFleetT(t, root, "init", "-q", "--bare", "-b", "main", origin)
	first := filepath.Join(root, "first")
	gitFleetT(t, root, "clone", "-q", origin, first)
	writeMeta(t, first, `{"_v": 1, "format": 1, "project_id": "prj_01a115b2e48f7cbda2b1840bd5f26c9d"}`+"\n")
	gitFleetT(t, first, "add", ".")
	gitFleetT(t, first, "commit", "-qm", "lore data")
	gitFleetT(t, first, "push", "-q", "origin", "main")
	stale := filepath.Join(root, "stale")
	gitFleetT(t, root, "clone", "-q", origin, stale)

	writeMeta(t, first, `{"_v": 1, "format": 2, "migrated_by": "Ana Test <ana@example.test>", "migrated_at": "2026-11-14T12:21:26Z", "min_lore_version": "0.1.18", "project_id": "prj_01a115b2e48f7cbda2b1840bd5f26c9d"}`+"\n")
	gitFleetT(t, first, "commit", "-qam", "lore: migrate rows to format 2")
	gitFleetT(t, first, "push", "-q", "origin", "main")
	ctx := context.Background()
	if _, ok := upstreamMigrated(ctx, stale); ok {
		t.Fatal("before the fetch the checkout cannot know: nothing to say")
	}

	gitFleetT(t, stale, "fetch", "-q", "origin")
	u, ok := upstreamMigrated(ctx, stale)
	if !ok || u.Ref != "origin/main" {
		t.Fatalf("after the fetch: %+v %v, want origin/main migrated", u, ok)
	}
	err := pullFirstErr(stale, u)
	if !errs.Has(err, errs.Refused) {
		t.Fatalf("migrate run in the stale checkout: %v, want E_REFUSED", err)
	}
	for _, want := range []string{"origin/main is already on the lore 0.1.18 format", "Ana Test", "git pull first", "do not migrate here"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q lacks %q", err, want)
		}
	}
	if line := formatOldLine(ctx, stale); !strings.Contains(line, "git pull first") {
		t.Fatalf("lore fix's line %q does not say pull", line)
	}
	if hint := formatOldMigrateHint(ctx, stale); !strings.Contains(hint, "git pull") || strings.Contains(hint, "&& lore fix --migrate") {
		t.Fatalf("fix --all's step %q, want git pull", hint)
	}

	gitFleetT(t, stale, "pull", "-q")
	if _, ok := upstreamMigrated(ctx, stale); ok {
		t.Fatal("once pulled the checkout is migrated: nothing to say")
	}
}
