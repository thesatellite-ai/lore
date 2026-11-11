package wire

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// copyRepoID is the repo id the copied checkout shares with the original.
const copyRepoID = "rep_0190e6a0c7d27b4c8a1e2f3a4b5c6d80"

// TestMigrateCopyLeavesMachineStore is the owner's scratch copy
// (2026-11-12): `migrate run` in a copied checkout must not put the
// original checkout's v0.1.17 machine-local rows into this machine's
// store. The copy's run names the rows it skipped and why; --machine-local
// copies them; a rerun copies nothing twice. The original checkout (the
// known_repos root) still copies.
func TestMigrateCopyLeavesMachineStore(t *testing.T) {
	t.Parallel()
	ctx := background()
	home := t.TempDir()
	original, scratch := gitRepo(t), gitRepo(t)
	const at = "'2026-01-02 03:04:05.123456789+00:00'"
	for _, dir := range []string{original, scratch} {
		if err := writeRepoID(dir); err != nil {
			t.Fatal(err)
		}
		writeV017DB(t, filepath.Join(dir, filepath.FromSlash(v017LoreDB)), v017RenderHistoriesDDL,
			`INSERT INTO render_histories VALUES ('`+copyRender+`', `+at+`, `+at+`, 'prj_x', NULL, '.lore/LORE.md', '# lore',
				'abc123', 6, NULL, '{"included":{"rules":1},"cut_summary":{}}', NULL)`)
	}
	orig := newTestAppHome(t, original, home)
	if err := orig.TouchRepo(ctx, onboard.KnownRepo{RepoID: copyRepoID, Root: original}); err != nil {
		t.Fatal(err)
	}
	cp := newTestAppHome(t, scratch, home)
	machineRows := func(a *App) int {
		eng, err := a.Engine(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := eng.List(ctx, store.SourceMachine, schema.TableRenderHistories)
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}

	steps, n, err := cp.migrateMachineLocal(ctx, false)
	if err != nil || n != 0 {
		t.Fatalf("copy: %d rows, %v; want none copied", n, err)
	}
	if !slices.ContainsFunc(steps, func(s string) bool {
		return strings.Contains(s, "skipped 1 render_histories rows of "+v017LoreDB) && strings.Contains(s, "this checkout is a copy: "+resolvedPath(original)) &&
			strings.Contains(s, "lore migrate run --machine-local")
	}) {
		t.Fatalf("the copy's run does not name what it skipped and why: %q", steps)
	}
	if got := machineRows(cp); got != 0 {
		t.Fatalf("the copy's run wrote %d render_histories into the machine store", got)
	}

	asked := opsmigrate.WithMachineLocal(ctx)
	if _, n, err = cp.migrateMachineLocal(asked, false); err != nil || n != 1 {
		t.Fatalf("--machine-local: %d rows, %v; want 1", n, err)
	}
	if _, n, err = cp.migrateMachineLocal(asked, false); err != nil || n != 0 {
		t.Fatalf("--machine-local rerun: %d rows, %v; want none (no duplicate)", n, err)
	}
	if steps, n, err = orig.migrateMachineLocal(ctx, false); err != nil || n != 0 || len(steps) != 0 {
		t.Fatalf("the original after the copy: %q, %d, %v; want nothing (copied once, same id)", steps, n, err)
	}
	if got := machineRows(orig); got != 1 {
		t.Fatalf("machine store holds %d render_histories, want exactly 1", got)
	}
}

// TestMigrateOwnCheckoutCopies: the original checkout (no other root of
// its repo id known, no setup folders) copies its rows as before.
func TestMigrateOwnCheckoutCopies(t *testing.T) {
	t.Parallel()
	ctx := background()
	repo := gitRepo(t)
	if err := writeRepoID(repo); err != nil {
		t.Fatal(err)
	}
	const at = "'2026-01-02 03:04:05.123456789+00:00'"
	writeV017DB(t, filepath.Join(repo, filepath.FromSlash(v017LoreDB)), v017RenderHistoriesDDL,
		`INSERT INTO render_histories VALUES ('`+copyRender+`', `+at+`, `+at+`, 'prj_x', NULL, '.lore/LORE.md', '# lore',
			'abc123', 6, NULL, '{"included":{"rules":1},"cut_summary":{}}', NULL)`)
	a := newTestAppHome(t, repo, t.TempDir())
	if err := a.TouchRepo(ctx, onboard.KnownRepo{RepoID: copyRepoID, Root: repo}); err != nil {
		t.Fatal(err)
	}
	if steps, n, err := a.migrateMachineLocal(ctx, false); err != nil || n != 1 {
		t.Fatalf("own checkout: %q, %d, %v; want 1 copied", steps, n, err)
	}
}

// newTestAppHome is newTestApp with a given home (two checkouts of one
// machine share its machine store).
func newTestAppHome(t *testing.T, dir, home string) *App {
	t.Helper()
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{RepoDir: dir, Home: home, Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}

// writeRepoID gives the checkout at dir the repo id copyRepoID (a cp -r
// copy carries its original's _meta.json).
func writeRepoID(dir string) error {
	data := filepath.Join(dir, filepath.FromSlash(rowfs.FolderDataPath(rowfs.FolderRoot)))
	if err := os.MkdirAll(data, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(data, rowfs.MetaFileName), []byte("{\"_v\": 1}\n"), 0o600); err != nil {
		return err
	}
	return rowfs.WriteRepoMeta(dir, rowfs.RepoMeta{RepoID: copyRepoID})
}
