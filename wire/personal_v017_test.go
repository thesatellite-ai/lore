package wire

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Fixed ids of a v0.1.17 local personal store fixture.
const (
	v017PersonalPrj = "prj_01a1a59b891c70b789144cd3e711fd01"
	v017PersonalMem = "mem_01a1a59b891c70b789144cd3e711fd02"
	v017OrgMem      = "mem_01a1a59b891c70b789144cd3e711fd03"
)

// writeV017Personal lays out ~/.lore/personal/<folder>/.lore/data with a
// project row and one v1 memory, as v0.1.17 wrote them.
func writeV017Personal(t *testing.T, home, folder, mem, body string) {
	t.Helper()
	data := filepath.Join(home, personalDirV017, folder, ".lore", "data")
	for _, d := range []string{schema.TableProjects, schema.TableMemories} {
		if err := os.MkdirAll(filepath.Join(data, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	prj := `{"_table":"projects","_v":1,"created_at":"2026-11-01T08:00:00Z","id":"` + v017PersonalPrj + `","name":"` + folder + `","updated_at":"2026-11-01T08:00:00Z"}`
	m := `{"_table":"memories","_v":1,"body":"` + body + `","created_at":"2026-11-01T08:00:00Z","id":"` + mem + `","kind":"retrieved","project_id":"` + v017PersonalPrj + `","source_kind":"manual","updated_at":"2026-11-01T08:00:00Z"}`
	for p, doc := range map[string]string{
		filepath.Join(data, schema.TableProjects, v017PersonalPrj+".json"): prj,
		filepath.Join(data, schema.TableMemories, mem+".json"):             m,
	} {
		if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// writeConfig writes ~/.lore/config.yaml.
func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestV017LocalPersonalStore: a v0.1.17 `personal: {store: local}` store
// is attached as `me` at the folder v0.1.17 files in (everywhere/ with no
// org), so `memory list --store me` shows its notes and `memory add --to
// me` works; `lore migrate run` flattens it (rows to the root with their
// old folder in _o, config.yaml switched to personal.store file://…, the
// old file kept as config.yaml.migrated), a fresh open reads the root, and
// a second run changes nothing.
func TestV017LocalPersonalStore(t *testing.T) {
	home := t.TempDir()
	cfg := "# my settings\npersonal:\n  store: local\n"
	writeConfig(t, home, cfg)
	writeV017Personal(t, home, personalEverywhereV017, v017PersonalMem, "old personal note")
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	repo := gitRepo(t)
	open := func() *App {
		a, nerr := New(Options{RepoDir: repo, Home: home, Actor: actor})
		if nerr != nil {
			t.Fatal(nerr)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	a := open()
	withProject(t, a)
	if !slices.Contains(a.Stores(), "me") {
		t.Fatalf("stores = %v: the local personal store is not attached as me", a.Stores())
	}
	out, err := run(t, a, opsmemory.OpList, `{"store":"me"}`, false)
	if err != nil || !strings.Contains(out, v017PersonalMem) {
		t.Fatalf("memory list --store me: %s %v", out, err)
	}
	out, err = run(t, a, opsmemory.OpAdd, `{"body":"new personal note","to":"me"}`, false)
	if err != nil {
		t.Fatalf("memory add --to me: %v", err)
	}
	newID := idOf(t, out)
	if _, err = os.Stat(filepath.Join(home, personalDirV017, personalEverywhereV017, ".lore", "data", schema.TableMemories, newID+".json")); err != nil {
		t.Fatalf("--to me did not file in everywhere/ (v0.1.17 placement): %v", err)
	}

	if out, err = run(t, a, opsmigrate.OpRun, `{"dry_run":true}`, false); err != nil || !strings.Contains(out, StepPersonalStoreFlatten) {
		t.Fatalf("dry run: %s %v", out, err)
	}
	if got := string(mustRead(t, filepath.Join(home, ConfigFile))); got != cfg {
		t.Fatalf("a dry run changed config.yaml: %q", got)
	}
	// everywhere/ holds the project row, two memories and this machine's
	// profile row (OV28 MACH-AUTO: refreshed into an own store after every
	// successful write, here the memory add --to me): the dry run counts
	// them and the run reports the same count (it moved them)
	const movedRows = 4
	if dry := migrateOut(t, out); dry.Count != movedRows {
		t.Fatalf("dry run count %d, want %d: %s", dry.Count, movedRows, out)
	}
	if out, err = run(t, a, opsmigrate.OpRun, `{}`, true); err != nil {
		t.Fatal(err)
	}
	if done := migrateOut(t, out); done.Count != movedRows {
		t.Fatalf("run count %d, want %d (what the dry run listed): %s", done.Count, movedRows, out)
	}
	for _, id := range []string{v017PersonalMem, newID} {
		raw := string(mustRead(t, filepath.Join(home, personalDirV017, ".lore", "data", schema.TableMemories, id+".json")))
		if !strings.Contains(raw, originOldFolder) || !strings.Contains(raw, personalEverywhereV017) {
			t.Fatalf("%s at the root without its old folder: %s", id, raw)
		}
		old := rowFile(t, filepath.Join(home, personalDirV017, personalEverywhereV017, ".lore", "data", schema.TableMemories, id+".json"))
		if string(old["_deleted"]) != "true" {
			t.Fatalf("%s not tombstoned in everywhere/: %v", id, old)
		}
	}
	got := string(mustRead(t, filepath.Join(home, ConfigFile)))
	if !strings.Contains(got, "store: file://") || !strings.Contains(got, "# my settings") {
		t.Fatalf("config.yaml after migrate: %q", got)
	}
	if kept := string(mustRead(t, filepath.Join(home, ConfigFile+migratedSuffix))); kept != cfg {
		t.Fatalf("config.yaml.migrated = %q", kept)
	}

	b := open()
	out, err = run(t, b, opsmemory.OpList, `{"store":"me"}`, false)
	if err != nil || !strings.Contains(out, v017PersonalMem) || !strings.Contains(out, newID) {
		t.Fatalf("after migrate, memory list --store me: %s %v", out, err)
	}
	out, err = run(t, b, opsmigrate.OpRun, `{}`, true)
	if err != nil || strings.Contains(out, StepPersonalStoreFlatten) || migrateOut(t, out).Count != 0 {
		t.Fatalf("second run: %s %v", out, err)
	}
}

// TestV017LocalPersonalPlacement: with an org resolved, `me` is the org's
// folder and everywhere/ is attached as me/everywhere (F17 load rule);
// with no personal store configured and no ~/.lore/personal, nothing is
// attached; a 0.1.18 personal.store url is not treated as v0.1.17's.
func TestV017LocalPersonalPlacement(t *testing.T) {
	home := t.TempDir()
	writeV017Personal(t, home, "agency", v017OrgMem, "agency note") // no config.yaml: v0.1.17's default is local
	writeV017Personal(t, home, personalEverywhereV017, v017PersonalMem, "everywhere note")
	org := func(context.Context) (*identity.Org, error) {
		return &identity.Org{Name: "agency", Identity: identity.Entry{Key: testEmail}}, nil
	}
	a := openWith(t, gitRepo(t), Options{Home: home, Org: org})
	withProject(t, a)
	for _, want := range []string{"me", "me/everywhere"} {
		if !slices.Contains(a.Stores(), storeName(want)) {
			t.Fatalf("stores = %v, missing %s", a.Stores(), want)
		}
	}
	if out, err := run(t, a, opsmemory.OpList, `{"store":"me"}`, false); err != nil || !strings.Contains(out, v017OrgMem) || strings.Contains(out, v017PersonalMem) {
		t.Fatalf("me in an org repo: %s %v", out, err)
	}
	if out, err := run(t, a, opsmemory.OpList, `{"store":"me/everywhere"}`, false); err != nil || !strings.Contains(out, v017PersonalMem) {
		t.Fatalf("me/everywhere: %s %v", out, err)
	}

	bare := openWith(t, gitRepo(t), Options{})
	if slices.Contains(bare.Stores(), "me") {
		t.Fatalf("a fresh home attached me: %v", bare.Stores())
	}
}

// storeName is a store.Source of s.
func storeName(s string) store.Source { return store.Source(s) }
