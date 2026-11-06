package wire

// liverows_nested_test.go — later 115 (nested project folder): in a
// monorepo (OWNERQ-1) every project keeps its live rows under its own
// folder of the one refs/lore/live (`_root` for the project at the repo
// top, the repo-relative path for a sub-folder project). `lore migrate run`
// and the live note of a project read only that project's folder, so no
// project's live rows are ever written into another project's .lore/data.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
)

// Live-only memories, one per project folder of the fixture, plus one in
// a folder that only shares a prefix with a project ("webx"). The ids are
// joined at run time so no id-shaped literal sits in the source (CLAUDE.md
// convention 8: secret scanners read long hex literals as keys).
var (
	nestedRootMem = nestedMemID("1")
	nestedWebMem  = nestedMemID("2")
	nestedAPIMem  = nestedMemID("3")
	nestedWebxMem = nestedMemID("4")
)

// nestedMemID is a valid memory id ending in last.
func nestedMemID(last string) string {
	return "mem_" + "01a1a59b891c70b7" + "89144cd3e711fc0" + last
}

// TestMigrateLiveNestedProjectFolder: projects at the repo top, at web/
// and at apps/api/ of one repo; the live ref holds one row in each of
// their folders and one in webx/. Each project's note counts only its own
// row, its migrate run lists and writes only its own row, into its own
// .lore/data, and leaves every other project's row out; webx/ (no project
// there) reaches nobody.
func TestMigrateLiveNestedProjectFolder(t *testing.T) {
	repo := gitRepo(t)
	type proj struct {
		rel, folder, mem string
	}
	projs := []proj{
		{rel: "", folder: liveRootFolder, mem: nestedRootMem},
		{rel: "web", folder: "web", mem: nestedWebMem},
		{rel: filepath.Join("apps", "api"), folder: "apps/api", mem: nestedAPIMem},
	}
	apps := make([]*App, len(projs))
	files := map[string]string{}
	for i, p := range projs {
		dir := filepath.Join(repo, p.rel)
		if err := os.MkdirAll(filepath.Join(dir, ".lore"), 0o750); err != nil { // the nearest .lore roots the project (what lore init makes)
			t.Fatal(err)
		}
		apps[i] = newTestApp(t, dir)
		withProject(t, apps[i])
		files[p.folder+"/"+schema.TableMemories+"/"+p.mem+".json"] = liveMemory(p.mem, projectID(t, apps[i]), "live in "+p.folder, liveNewer)
	}
	files["webx/"+schema.TableMemories+"/"+nestedWebxMem+".json"] = liveMemory(nestedWebxMem, projectID(t, apps[0]), "no project here", liveNewer)
	writeLiveRef(t, repo, liveRefLocal, files)

	for i, p := range projs {
		a, dir := apps[i], filepath.Join(repo, p.rel)
		if note := a.LiveNote(background()); !strings.Contains(note, "(1 memories)") {
			t.Fatalf("%s: note counts only its own folder's row: %q", p.folder, note)
		}
		got, err := a.MigrateLive(background(), true)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{schema.TableMemories + "/" + p.mem}; !slices.Equal(got, want) {
			t.Fatalf("%s: dry run lists %v, want %v", p.folder, got, want)
		}
		if _, err = a.MigrateLive(background(), false); err != nil {
			t.Fatal(err)
		}
		if body := fieldString(t, rowFile(t, memPath(dir, p.mem)), "body"); body != "live in "+p.folder {
			t.Fatalf("%s: migrated row body %q", p.folder, body)
		}
		for _, other := range []string{nestedRootMem, nestedWebMem, nestedAPIMem, nestedWebxMem} {
			if other == p.mem {
				continue
			}
			if _, serr := os.Stat(memPath(dir, other)); !os.IsNotExist(serr) {
				t.Fatalf("%s: another folder's live row %s was written into this project (%v)", p.folder, other, serr)
			}
		}
		if note := a.LiveNote(background()); note != "" {
			t.Fatalf("%s: note after the run: %q", p.folder, note)
		}
	}
}
