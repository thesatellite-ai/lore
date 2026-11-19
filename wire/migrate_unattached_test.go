package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// unattachedStoreRepo is a repo attaching the folder store "northwind" at
// its root, whose folder "billing" (not attached: a lore 0.1.17
// `--to <org>/billing --create` made it) holds one `_v: 1` memory, as a
// 0.1.17 store shape leaves it. It returns the repo and the row file.
func unattachedStoreRepo(t *testing.T) (repo, file string) {
	t.Helper()
	dir := t.TempDir()
	sid, err := ids.Parse[ids.Store](folderStore)
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	const prj = "prj_dada87779c777e1899961f60829dc2d9"
	meta := filepath.Join(dir, filepath.FromSlash(rowfs.LayoutStore.StorePath("billing", rowfs.MetaFileName)))
	if err = os.MkdirAll(filepath.Dir(meta), 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(meta, []byte(`{"_v":1,"project_id":"`+prj+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	def, ok := mustReg(t).Table(schema.TableMemories)
	if !ok {
		t.Fatal("no memories table")
	}
	body, _ := v1Body(t, def, prj)
	const id = "mem_01a1dc0d2dae7306a6ad9f99e786245d"
	file = filepath.Join(dir, filepath.FromSlash(rowfs.LayoutStore.StorePath("billing", string(schema.TableMemories)+"/"+id+".json")))
	if err = os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		t.Fatal(err)
	}
	doc := `{"_table":"memories","_v":1,"created_at":"2026-11-04T08:09:56.252002659Z","id":"` + id + `",` + body + `,"updated_at":"2026-11-04T08:09:56.252002894Z"}`
	if err = os.WriteFile(file, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	repo = gitRepo(t)
	writeStoresYAML(t, repo, "stores:\n  - name: northwind\n    url: file://"+filepath.ToSlash(dir)+"\n")
	return repo, file
}

// TestMigrateUnattachedFolder (devco P-9): a `_v: 1` row in a folder of an
// attached store that the repo does not attach is named by migrate status
// (its own place and a pending line with `lore migrate run`) and by doctor
// (row_format warning, fix `lore migrate run`, never "a lore older than
// 0.1.18"); the dry run lists it and writes nothing; the run rewrites it
// to `_v: 2`; afterwards status has nothing pending and doctor is quiet.
func TestMigrateUnattachedFolder(t *testing.T) {
	t.Parallel()
	repo, file := unattachedStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	ctx := background()

	st, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(st.Places, func(p opsmigrate.PlaceStatus) bool { return p.Place == "northwind/billing" && p.V1 == 1 }) {
		t.Fatalf("status places miss northwind/billing with 1 v1 row: %+v", st.Places)
	}
	if !slices.ContainsFunc(st.Pending, func(l string) bool {
		return strings.HasPrefix(l, "northwind/billing (") && strings.Contains(l, "memories 1 row at v1; lore migrate run upgrades them")
	}) {
		t.Fatalf("status pending does not name the folder: %q", st.Pending)
	}
	found, err := a.DoctorRowFormat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Level != DoctorWarning || found[0].Fix != "lore migrate run" ||
		!strings.Contains(found[0].Message, "northwind/billing") || strings.Contains(found[0].Message, "older than 0.1.18") {
		t.Fatalf("doctor row_format = %+v", found)
	}

	raw, err := run(t, a, opsmigrate.OpRun, `{"dry_run":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry := migrateOut(t, raw); !slices.ContainsFunc(dry.Rows, func(l string) bool { return strings.Contains(l, "mem_01a1dc0d2dae7306a6ad9f99e786245d") }) {
		t.Fatalf("dry run does not list the folder's row: %v", dry.Rows)
	}
	if v := string(rowFile(t, file)["_v"]); v != "1" {
		t.Fatalf("the dry run rewrote the file: _v %s", v)
	}

	if _, err = run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	var after struct {
		V    int    `json:"_v"`
		Body string `json:"body"`
	}
	if err = json.Unmarshal(mustRead(t, file), &after); err != nil || after.V != 2 {
		t.Fatalf("the run left the unattached folder's row at _v %d (%v)", after.V, err)
	}
	if st, err = a.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if len(st.Pending) != 0 {
		t.Fatalf("status still pending after the run: %q", st.Pending)
	}
	if found, err = a.DoctorRowFormat(ctx); err != nil || len(found) != 0 {
		t.Fatalf("doctor row_format after the run = %+v, %v", found, err)
	}
}

// TestDoctorRowFormatStoreLeftovers (devco P-9, second defect): v1 rows of
// a migrated repo that sit only in an attached store are named as rows a
// migration has not converted, with `lore migrate run`; v1 rows in the
// repo's own place keep the older-lore warning path.
func TestDoctorRowFormatStoreLeftovers(t *testing.T) {
	t.Parallel()
	got := v1StorePlaces([]opsmigrate.PlaceStatus{{Place: "repo", V2: 4}, {Place: "orgmg", V1: 2, V2: 3}})
	if len(got) != 1 || got[0] != "orgmg 2 rows" {
		t.Fatalf("store leftovers = %q", got)
	}
	if got = v1StorePlaces([]opsmigrate.PlaceStatus{{Place: "repo", V1: 1}, {Place: "orgmg", V1: 2}}); got != nil {
		t.Fatalf("a v1 row in the repo is not a store leftover: %q", got)
	}
	own, folders := splitUnattached([]string{"memories: 1 row at v1", "a/b (" + unattachedTag + "): memories 1 row at v1"})
	if len(own) != 1 || len(folders) != 1 {
		t.Fatalf("split = %q %q", own, folders)
	}
}
