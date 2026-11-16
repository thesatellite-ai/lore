package wire

// firstwrites_race_test.go — devco D-437 part 2: the first parallel
// writes into a new store folder failed E_STALE ("row changed since it
// was read") on the folder's project row. Every first writer creates that
// row under the folder's natural id; the ones that listed the folder's
// rows before another process made it lost the race and failed their own
// write.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestFirstWritesIntoANewFolderRace: two lore processes (Apps), both open
// before either writes, each write a first row into the same new folder.
// The second one's folder project row was made by the first after it read
// the folder: it uses that row, its write lands, no E_STALE.
func TestFirstWritesIntoANewFolderRace(t *testing.T) {
	t.Parallel()
	repo := folderTargetRepo(t)
	first := newTestApp(t, repo)
	withProject(t, first)
	home := first.opts.Home
	second := openWith(t, repo, Options{Home: home})
	for _, a := range []*App{first, second} {
		if _, err := a.Engine(background()); err != nil {
			t.Fatal(err)
		}
		// each process has listed the folder's (no) project rows
		st, err := a.state(background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = st.eng.List(background(), store.Source("northwind/acme"), schema.TableProjects); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := run(t, first, opsmemory.OpAdd, `{"body":"first writer","to":"northwind/acme"}`, true); err != nil {
		t.Fatalf("the first writer: %v", err)
	}
	// the second process listed the folder before the first one wrote and
	// found no project row: it creates the row under the folder's id
	st, err := second.state(background())
	if err != nil {
		t.Fatal(err)
	}
	target := store.Source("northwind/acme")
	pid, err := folderProjectIDOf(background(), st, target)
	if err != nil || pid.IsZero() {
		t.Fatalf("the folder has no natural project id (the case under test): %v", err)
	}
	n, err := value.TextOf("acme")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := second.createFolderProject(background(), st, target, pid, map[string]value.Value{rosterFieldName: n})
	if err != nil {
		t.Fatalf("the second first writer into the folder failed (it lost the race for the folder's project row): %v", err)
	}
	if ref.ID() != pid.Any() {
		t.Fatalf("the second writer's project row = %v, want the folder's %v", ref, pid)
	}
	if _, err = run(t, second, opsmemory.OpAdd, `{"body":"second writer","to":"northwind/acme"}`, true); err != nil {
		t.Fatalf("the second writer's row: %v", err)
	}
}

// TestParallelFirstWritesIntoANewFolder: N writers (Apps, as N lore
// processes) start together on one new folder; every write lands and
// none is E_STALE.
func TestParallelFirstWritesIntoANewFolder(t *testing.T) {
	t.Parallel()
	repo := folderTargetRepo(t)
	lead := newTestApp(t, repo)
	withProject(t, lead)
	home := lead.opts.Home
	const n = 6
	apps := make([]*App, n)
	for i := range apps {
		apps[i] = openWith(t, repo, Options{Home: home})
		if _, err := apps[i].Engine(background()); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	fails := make([]error, n)
	for i, a := range apps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, fails[i] = run(t, a, opsmemory.OpAdd, `{"body":"parallel writer `+string(rune('a'+i))+`","to":"northwind/acme"}`, true)
		}()
	}
	wg.Wait()
	var bad []string
	for i, err := range fails {
		if err != nil {
			bad = append(bad, string(rune('a'+i))+": "+err.Error())
			if errs.Has(err, errs.Stale) {
				bad = append(bad, "(E_STALE)")
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("parallel first writers into a new folder failed:\n%s", strings.Join(bad, "\n"))
	}
}

// folderTargetRepo is folderStoreRepo with the target northwind/acme
// recorded in stores.yaml (as a first `--to northwind/acme` records it),
// so every App attaches the folder before anything is written there.
func folderTargetRepo(t *testing.T) string {
	t.Helper()
	repo, storeDir, yamlPath, yaml := folderStoreRepo(t)
	yaml += "  - name: northwind/acme\n    url: file://" + filepath.ToSlash(storeDir) + "\n    folder: acme\n"
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	// a folder made by `lore org` or a first write records its permanent
	// project id: the natural id every first writer creates the row under
	meta := `{"_v":1,"project_id":"prj_01a1e1dcbaa3772badbf3f23c68a8dc6"}` + "\n"
	if err := os.WriteFile(filepath.Join(storeDir, "acme", rowfs.MetaFileName), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}
