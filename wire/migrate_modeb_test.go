package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// Fixed ids of the Mode B fixture (the 32 hex digits are arbitrary).
const (
	modeBProject = "prj_01a1a5aca7f57659be7320c5f4b70a01"
	modeBMemA    = "mem_01a1a5aca7f57659be7320c5f4b70a02"
	modeBMemB    = "mem_01a1a5aca7f57659be7320c5f4b70a03"
)

// fakeModeB is the binary's ModeBExporter: it returns exp and records the
// pointer it was asked about.
type fakeModeB struct {
	exp   ModeBExport
	asked []string
}

func (f *fakeModeB) ExportModeB(_ context.Context, pointer string) (ModeBExport, error) {
	f.asked = append(f.asked, pointer)
	return f.exp, nil
}

// v1Doc is a v0.1.17 row document of table with the given columns.
func v1Doc(t *testing.T, table string, cols map[string]any) []byte {
	t.Helper()
	m := map[string]any{"_table": table, "_v": 1, "created_at": "2026-11-04T08:09:55Z", "updated_at": "2026-11-04T08:09:55Z"}
	for k, v := range cols {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// modeBRepo is a git repo whose .lore holds only a Mode B pointer, and an
// App over it whose exporter returns a project with two memories.
func modeBRepo(t *testing.T) (repo, storeDir, pointer string, a func() *App, fake *fakeModeB) {
	t.Helper()
	repo = gitRepo(t)
	pointer = filepath.Join(repo, ".lore", "lore.yaml")
	if err := os.MkdirAll(filepath.Dir(pointer), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pointer, []byte("db: ~/shared/lore.db\nproject_id: "+modeBProject+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shared := t.TempDir()
	fake = &fakeModeB{exp: ModeBExport{
		DBPath: filepath.Join(shared, "lore.db"), ProjectID: modeBProject, ProjectName: "infra",
		Docs: [][]byte{
			v1Doc(t, schema.TableProjects, map[string]any{"id": modeBProject, "name": "infra"}),
			v1Doc(t, schema.TableMemories, map[string]any{"id": modeBMemA, "body": "the staging cluster sleeps", "kind": "retrieved", "project_id": modeBProject, "source_kind": "manual"}),
			v1Doc(t, schema.TableMemories, map[string]any{"id": modeBMemB, "body": "deploys freeze on friday", "kind": "retrieved", "project_id": modeBProject, "source_kind": "manual"}),
		},
	}}
	storeDir = filepath.Join(shared, modeBStoresDir, "infra")
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	a = func() *App {
		t.Helper()
		app, err := New(Options{RepoDir: repo, Home: home, Actor: actor, ModeB: fake})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = app.Close() }) // test teardown: a close error has nothing left to report to
		return app
	}
	return repo, storeDir, pointer, a, fake
}

// migrateOut decodes a migrate.run result.
func migrateOut(t *testing.T, raw string) opsmigrate.RunOut {
	t.Helper()
	var out opsmigrate.RunOut
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("migrate output %s: %v", raw, err)
	}
	return out
}

// TestModeBMigration (Q-MODEB): in a Mode B repo every other command is
// refused naming `lore migrate run`; --dry-run lists one step per row plus
// the stores.yaml and pointer steps, counts every row and changes nothing;
// the run writes each row as a `_v: 2` file into <db dir>/stores/<project>,
// adds the store to .lore/stores.yaml as "shared", keeps the pointer as
// lore.yaml.migrated (same bytes), and reports the same count; the rows
// are then readable through the store; a re-run is a no-op (count 0, no
// steps).
func TestModeBMigration(t *testing.T) {
	t.Parallel()
	repo, storeDir, pointer, open, fake := modeBRepo(t)
	pointerBytes := mustRead(t, pointer)
	a := open()
	if _, err := run(t, a, opsmemory.OpList, `{}`, false); !errs.Has(err, errs.Unsupported) || !strings.Contains(err.Error(), MigrateCommand) {
		t.Fatalf("a Mode B repo must refuse other commands naming %s: %v", MigrateCommand, err)
	}
	const wantRows = 3 // the project row and two memories
	raw, err := run(t, a, opsmigrate.OpRun, `{"dry_run":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	dry := migrateOut(t, raw)
	if dry.Count != wantRows || len(dry.Steps) != wantRows+2 {
		t.Fatalf("dry run: %+v, want count %d and %d steps", dry, wantRows, wantRows+2)
	}
	for _, id := range []string{modeBProject, modeBMemA, modeBMemB} {
		if !strings.Contains(raw, id) {
			t.Fatalf("dry run does not list %s: %s", id, raw)
		}
	}
	if _, serr := os.Stat(storeDir); !os.IsNotExist(serr) {
		t.Fatalf("a dry run made the store folder: %v", serr)
	}
	if _, serr := os.Stat(pointer); serr != nil {
		t.Fatalf("a dry run moved the pointer: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(repo, ".lore", "stores.yaml")); !os.IsNotExist(serr) {
		t.Fatalf("a dry run wrote stores.yaml: %v", serr)
	}

	raw, err = run(t, a, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := migrateOut(t, raw); got.Count != wantRows || len(got.Steps) != len(dry.Steps) {
		t.Fatalf("run: %+v, want the dry run's count %d", got, wantRows)
	}
	for _, c := range []struct{ table, id string }{{schema.TableProjects, modeBProject}, {schema.TableMemories, modeBMemA}, {schema.TableMemories, modeBMemB}} {
		f := rowFile(t, filepath.Join(storeDir, rowfs.StoreDataDir, c.table, c.id+".json"))
		if string(f["_v"]) != "2" || len(f["_hlc"]) == 0 {
			t.Fatalf("%s/%s is not a _v 2 row: %v", c.table, c.id, f)
		}
	}
	cfg := string(mustRead(t, filepath.Join(repo, ".lore", "stores.yaml")))
	if !strings.Contains(cfg, "name: "+ModeBStoreName) || !strings.Contains(cfg, "file://") {
		t.Fatalf("stores.yaml has no %s entry: %q", ModeBStoreName, cfg)
	}
	if _, serr := os.Stat(pointer); !os.IsNotExist(serr) {
		t.Fatalf("the pointer is still in place: %v", serr)
	}
	if kept := mustRead(t, pointer+ModeBMigratedSuffix); string(kept) != string(pointerBytes) {
		t.Fatalf("lore.yaml.migrated = %q, want the pointer's bytes", kept)
	}

	b := open()
	out, err := run(t, b, opsmemory.OpList, `{"store":"`+ModeBStoreName+`"}`, false)
	if err != nil || !strings.Contains(out, modeBMemA) || !strings.Contains(out, modeBMemB) {
		t.Fatalf("after migrate, memory list --store shared: %s %v", out, err)
	}
	raw, err = run(t, b, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if again := migrateOut(t, raw); again.Count != 0 || len(again.Steps) != 0 {
		t.Fatalf("second run must be a no-op: %+v", again)
	}
	if len(fake.asked) != 2 {
		t.Fatalf("the exporter was asked %d times, want 2 (dry run + run; the re-run finds no pointer)", len(fake.asked))
	}
}

// TestModeBResumedRunWritesOnlyMissingRows: a run cut short after the
// store got some rows (here: all of them, and the pointer is still in
// place) resumes without overwriting: count is the rows written this time
// (0), the store entry is not added twice, and the pointer is then kept.
func TestModeBResumedRunWritesOnlyMissingRows(t *testing.T) {
	t.Parallel()
	repo, _, pointer, open, _ := modeBRepo(t)
	pointerBytes := mustRead(t, pointer)
	if _, err := run(t, open(), opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	// put the pointer back as if the rename had not happened
	if err := os.Rename(pointer+ModeBMigratedSuffix, pointer); err != nil {
		t.Fatal(err)
	}
	raw, err := run(t, open(), opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := migrateOut(t, raw); got.Count != 0 {
		t.Fatalf("resumed run: %+v, want count 0 (every row was already there)", got)
	}
	if cfg := string(mustRead(t, filepath.Join(repo, ".lore", "stores.yaml"))); strings.Count(cfg, "name: "+ModeBStoreName) != 1 {
		t.Fatalf("the store entry must be added once: %q", cfg)
	}
	if kept := mustRead(t, pointer+ModeBMigratedSuffix); string(kept) != string(pointerBytes) {
		t.Fatalf("pointer not kept: %q", kept)
	}
}

// TestModeBRollbackRefused (BUGFIX-07): `lore migrate rollback` after a
// run that converted Mode B refuses with E_UNSUPPORTED naming the
// conversion and changes nothing: the pointer stays lore.yaml.migrated,
// stores.yaml keeps the shared entry, the store keeps exactly its rows,
// and the journal stays "done" (status says it cannot be rolled back).
// Before the fix the rollback restored nothing (the journal's only place
// is the repo's empty .lore/data), reported success with empty lines and
// marked the journal rolled back.
func TestModeBRollbackRefused(t *testing.T) {
	t.Parallel()
	repo, storeDir, pointer, open, _ := modeBRepo(t)
	raw, err := run(t, open(), opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := migrateOut(t, raw); !slices.Contains(got.Journal, modeBNoUndoLine) {
		t.Fatalf("a Mode B run must end with %q, not the undo line: %v", modeBNoUndoLine, got.Journal)
	}
	storesYAML := filepath.Join(repo, ".lore", "stores.yaml")
	cfgBefore := string(mustRead(t, storesYAML))
	rowsBefore := storeRowFiles(t, storeDir)
	_, err = run(t, open(), opsmigrate.OpRollback, `{}`, false)
	if !errs.Has(err, errs.Unsupported) || !strings.Contains(err.Error(), "cannot be rolled back") {
		t.Fatalf("rollback after a Mode B conversion: %v, want E_UNSUPPORTED saying it cannot be rolled back", err)
	}
	if _, serr := os.Stat(pointer + ModeBMigratedSuffix); serr != nil {
		t.Fatalf("the refused rollback moved the kept pointer: %v", serr)
	}
	if got := string(mustRead(t, storesYAML)); got != cfgBefore {
		t.Fatalf("the refused rollback changed stores.yaml: %q, was %q", got, cfgBefore)
	}
	if got := storeRowFiles(t, storeDir); !slices.Equal(got, rowsBefore) {
		t.Fatalf("the refused rollback changed the store's rows: %v, was %v", got, rowsBefore)
	}
	raw, err = run(t, open(), opsmigrate.OpStatus, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var st opsmigrate.StatusOut
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Journal, "cannot be rolled back") {
		t.Fatalf("status journal after a Mode B run: %q, want it to say the conversion cannot be rolled back", st.Journal)
	}
}

// storeRowFiles lists the row files of a folder store's root folder (its table folders at its top).
func storeRowFiles(t *testing.T, storeDir string) []string {
	t.Helper()
	var out []string
	root := storeDir
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		out = append(out, rel)
		return rerr
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMigrateRollbackAndUnlockWriteNoMachineRows (BUGFIX-07): migrate
// rollback and unlock write no actor or machine row (no MACH-AUTO profile
// write after them). Before the fix a rollback of the shared store left
// the caller's actor and machine rows in it, so `lore migrate status`
// counted the store at 4-5 rows where the rollback had restored 2.
func TestMigrateRollbackAndUnlockWriteNoMachineRows(t *testing.T) {
	t.Parallel()
	_, storeDir, _, open, _ := modeBRepo(t)
	if _, err := run(t, open(), opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	// a v0.1.17 row file in the shared store: the next run rewrites it,
	// so its journal has the store as a place a rollback restores
	const v1Mem = "mem_01a1a5aca7f57659be7320c5f4b70a04"
	v1Path := filepath.Join(storeDir, rowfs.StoreDataDir, schema.TableMemories, v1Mem+".json")
	if err := os.WriteFile(v1Path, v1Doc(t, schema.TableMemories, map[string]any{"id": v1Mem, "body": "the backups run at noon", "kind": "retrieved", "project_id": modeBProject, "source_kind": "manual"}), 0o600); err != nil {
		t.Fatal(err)
	}
	before := storeRowFiles(t, storeDir)
	if _, err := run(t, open(), opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, open(), opsmigrate.OpRollback, `{}`, false); err != nil {
		t.Fatal(err)
	}
	if got := storeRowFiles(t, storeDir); !slices.Equal(got, before) {
		t.Fatalf("after rollback the store holds %v, want exactly %v (no actor or machine rows)", got, before)
	}
	if _, err := run(t, open(), opsmigrate.OpUnlock, `{}`, false); err != nil {
		t.Fatal(err)
	}
	if got := storeRowFiles(t, storeDir); !slices.Equal(got, before) {
		t.Fatalf("after unlock the store holds %v, want exactly %v (no actor or machine rows)", got, before)
	}
}

// TestModeBProjectAfterConversion (owner item 36): a converted Mode B repo
// keeps no project row in its repo store (the rows became the "shared"
// folder store), and lore fix's render step after the conversion failed
// with "this repository has no lore project". The project is the kept
// pointer's project_id, found in the attached store: in the same process
// right after lore fix's migrate step (the App reopens with the store the
// conversion attached), and in a new one.
func TestModeBProjectAfterConversion(t *testing.T) {
	t.Parallel()
	repo, _, _, open, _ := modeBRepo(t)
	a := open()
	ctx := background()
	if st := a.fixMigrate(ctx, repo, false, false); st.Failed || !st.Changed {
		t.Fatalf("migrate step: %+v", st)
	}
	if pid, err := a.Project(ctx); err != nil || pid.String() != modeBProject {
		t.Fatalf("same process after the conversion: project %q, %v; want %s", pid, err, modeBProject)
	}
	if pid, err := open().Project(ctx); err != nil || pid.String() != modeBProject {
		t.Fatalf("a new process: project %q, %v; want %s", pid, err, modeBProject)
	}
}
