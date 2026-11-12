package git

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// openFlat opens the store clone of remote at dir on folder.
func openFlat(t *testing.T, remote, dir, folder string) *Backend {
	t.Helper()
	b, err := OpenClone(context.Background(), remote, dir, folder, store.OpenOptions{}, WithMerger(newestMerger{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// TestFlattenLayout (brief 1.2, 1.6): one commit moves every row from
// <folder>/.lore/data to <folder>, the org config out of org/.lore, sets
// lore_store 4, writes the README; the root folder's rows land in _store/
// (D-ad); the clone is clean after it, a second run does nothing, and the
// rows read at the same ids.
func TestFlattenLayout(t *testing.T) {
	ctx := context.Background()
	src := newOldRemote(t, "org", []string{"team", "."}, map[string][]byte{"org/.lore/lore-org.yaml": []byte("name: acme\n"), ".lore/lock": nil})
	dir := filepath.Join(t.TempDir(), "stores", "acme", "notes")
	b := openFlat(t, src.remote, dir, "team")
	if !b.NeedsFlatten() || !NeedsFlattenAt(dir) {
		t.Fatal("an old-layout clone does not need the layout commit")
	}
	res, err := b.FlattenLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit == "" || res.Moved != 3 || !slices.Equal(res.Folders, []string{".", "org", "team"}) || !slices.Equal(res.Untracked, []string{".lore/lock"}) {
		t.Fatalf("flatten: %+v", res)
	}
	if subject := gitT(t, dir, "log", "-1", "--format=%s"); subject != FlattenSubject {
		t.Fatalf("commit subject %q", subject)
	}
	for p := range src.rows {
		flat, ok := FlatPath(p)
		if !ok {
			t.Fatalf("no flat path for %s", p)
		}
		if got := readFile(t, filepath.Join(dir, filepath.FromSlash(flat))); got != string(src.rows[p]) {
			t.Fatalf("%s moved to %s with other bytes", p, flat)
		}
	}
	if got := readFile(t, filepath.Join(dir, "org", "lore-org.yaml")); got != "name: acme\n" {
		t.Fatalf("org config %q", got)
	}
	if strings.Contains(gitT(t, dir, "ls-files"), ".lore/lock") {
		t.Fatal("the committed .lore/lock is still tracked")
	}
	for _, gone := range []string{".lore/data", "team/.lore", "org/.lore"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(gone))); err == nil {
			t.Errorf("%s is left", gone)
		}
	}
	m, err := rowfs.ReadMarker(dir)
	if err != nil || m.Version != rowfs.MarkerVersion || m.OrgFolder != "org" {
		t.Fatalf("marker %+v %v", m, err)
	}
	if !rowfs.HasReadme(dir) {
		t.Fatal("no README.md")
	}
	if st := gitT(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("clone not clean after the flatten:\n%s", st)
	}
	if b.NeedsFlatten() {
		t.Fatal("NeedsFlatten after the flatten")
	}
	if again, err := b.FlattenLayout(ctx); err != nil || again.Commit != "" {
		t.Fatalf("second flatten: %+v %v", again, err)
	}
	r := openFlat(t, src.remote, dir, "team")
	if r.folder.OldLayout() {
		t.Fatal("the team folder still opens in the old layout")
	}
	if got := get(t, r, src.ids[0]); got.ID != src.ids[0] {
		t.Fatalf("row %s reads as %s", src.ids[0], got.ID)
	}
}

// TestFlattenFixesOrgFolder: a marker naming an org folder the store does
// not hold loses org_folder in the layout commit.
func TestFlattenFixesOrgFolder(t *testing.T) {
	src := newOldRemote(t, "org", []string{"team"}, nil)
	dir := filepath.Join(t.TempDir(), "stores", "acme", "notes")
	b := openFlat(t, src.remote, dir, "team")
	if _, err := b.FlattenLayout(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := rowfs.ReadMarker(dir)
	if err != nil || m.OrgFolder != "" {
		t.Fatalf("marker %+v %v, want no org_folder", m, err)
	}
	if strings.Contains(gitT(t, dir, "show", "HEAD:"+rowfs.MarkerFile), "org_folder") {
		t.Fatal("the committed marker still names org_folder")
	}
}

// TestFlattenKeepsHandEdits: an uncommitted edit of a row moves with the
// row and stays uncommitted; the commit holds the committed bytes.
func TestFlattenKeepsHandEdits(t *testing.T) {
	src := newOldRemote(t, "", []string{"team"}, nil)
	dir := filepath.Join(t.TempDir(), "stores", "acme", "notes")
	b := openFlat(t, src.remote, dir, "team")
	var old string
	for p := range src.rows {
		old = p
	}
	writeFile(t, filepath.Join(dir, filepath.FromSlash(old)), "edited by hand\n")
	if _, err := b.FlattenLayout(context.Background()); err != nil {
		t.Fatal(err)
	}
	flat, _ := FlatPath(old)
	if got := readFile(t, filepath.Join(dir, filepath.FromSlash(flat))); got != "edited by hand\n" {
		t.Fatalf("hand edit lost: %q", got)
	}
	if got := gitT(t, dir, "show", "HEAD:"+flat); got != strings.TrimSpace(string(src.rows[old])) {
		t.Fatalf("the commit holds the hand edit: %q", got)
	}
	if st := gitT(t, dir, "status", "--porcelain"); st != "M "+flat {
		t.Fatalf("status %q, want only the edited row modified", st)
	}
}

// TestFlattenResumes: a run cut short after some files moved on disk (the
// pending mark written, the index not changed) is finished by the next.
func TestFlattenResumes(t *testing.T) {
	src := newOldRemote(t, "", []string{"team", "ops"}, nil)
	dir := filepath.Join(t.TempDir(), "stores", "acme", "notes")
	b := openFlat(t, src.remote, dir, "team")
	writeFile(t, filepath.Join(StateDirOf(dir), flattenPendingFile), "")
	for p := range src.rows {
		flat, _ := FlatPath(p)
		writeFile(t, filepath.Join(dir, filepath.FromSlash(flat)), string(src.rows[p]))
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
		break // one moved, the other not
	}
	res, err := b.FlattenLayout(context.Background())
	if err != nil || res.Commit == "" || res.Moved != 2 {
		t.Fatalf("resumed flatten: %+v %v", res, err)
	}
	if st := gitT(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("clone not clean after the resumed flatten:\n%s", st)
	}
	files := gitT(t, dir, "ls-files")
	if strings.Contains(files, ".lore/") {
		t.Fatalf("old paths still tracked:\n%s", files)
	}
	if NeedsFlattenAt(dir) {
		t.Fatal("still pending after the resumed flatten")
	}
}

// TestFlattenBothPlaces: a row at both places (a teammate's older lore
// wrote it into the old place after the move) is merged into one.
func TestFlattenBothPlaces(t *testing.T) {
	g := newGen(t)
	id := newID(t)
	v1 := g.row(id, "first", hlc.HLC{})
	v2 := g.row(id, "second", v1.HLC)
	plain := rowfs.LayoutStore.StorePath("team", rowfs.RowPath(memories, id))
	oldP := rowfs.LayoutRepo.StorePath("team", rowfs.RowPath(memories, id))
	src := newOldRemote(t, "", nil, map[string][]byte{plain: v1.Doc, oldP: v2.Doc})
	dir := filepath.Join(t.TempDir(), "stores", "acme", "notes")
	b := openFlat(t, src.remote, dir, "team")
	res, err := b.FlattenLayout(context.Background())
	if err != nil || len(res.Merged) != 1 || res.Merged[0] != plain {
		t.Fatalf("flatten: %+v %v", res, err)
	}
	if got := gitT(t, dir, "show", "HEAD:"+plain); !strings.Contains(got, "second") {
		t.Fatalf("merged row %q, want the newer edit", got)
	}
	if st := gitT(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("clone not clean:\n%s", st)
	}
}

// TestFlatPath maps the old paths and leaves the rest.
func TestFlatPath(t *testing.T) {
	for in, want := range map[string]string{
		".lore/data/memories/m.json":          "_store/memories/m.json",
		".lore/data/_meta.json":               "_store/_meta.json",
		"team/.lore/data/tasks/t.json":        "team/tasks/t.json",
		"team/web/.lore/data/_meta.json":      "team/web/_meta.json",
		"org/.lore/lore-org.yaml":             "org/lore-org.yaml",
		".lore/lore-org.yaml":                 "lore-org.yaml",
		".lore/lock":                          "",
		"team/memories/m.json":                "",
		"notes/.lore/state/journal/x.journal": "",
	} {
		got, ok := FlatPath(in)
		if ok != (want != "") || got != want {
			t.Errorf("FlatPath(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
}

// TestNeedsFlattenAtOnlyStores: a clone of a repository that is not a lore
// store (no lore-store.yaml; a code repo with its own .lore/data, listed
// under stores: by mistake) is never a layout-move candidate, so the
// first-run step does not open it and warn on every command; the same tree
// with the marker is one.
func TestNeedsFlattenAtOnlyStores(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, p := range []string{filepath.Join(dir, ".git"), filepath.Join(dir, ".lore", "data", "memories")} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if NeedsFlattenAt(dir) {
		t.Fatal("a clone without lore-store.yaml is a flatten candidate")
	}
	if err := os.WriteFile(filepath.Join(dir, rowfs.MarkerFile), []byte("lore_store: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !NeedsFlattenAt(dir) {
		t.Fatal("a store in the old layout is not a flatten candidate")
	}
}

// newFlatRemote seeds a bare remote with a store of the first plain layout
// (lore_store: 3): one memory row in each of folders, the root folder's
// ("." ) as <table>/<id>.json at the store's top, its _meta.json there too,
// plus the extra files given.
func newFlatRemote(t *testing.T, folders []string, extra map[string][]byte) oldStore {
	t.Helper()
	dir := t.TempDir()
	o := oldStore{remote: filepath.Join(dir, "remote.git"), rows: map[string][]byte{}}
	gitT(t, dir, "init", "--quiet", "--bare", "--initial-branch="+testBranch, o.remote)
	seed := filepath.Join(dir, "seed")
	gitT(t, dir, "init", "--quiet", "--initial-branch="+testBranch, seed)
	marker := strings.Replace(string(oldMarker(t, "")), "lore_store: 2", "lore_store: 3", 1)
	files := map[string][]byte{rowfs.MarkerFile: []byte(marker)}
	g := newGen(t)
	for _, f := range folders {
		id := newID(t)
		r := g.row(id, "note in "+f, hlc.HLC{})
		p := path.Join(f, rowfs.RowPath(memories, id)) // "." joins to the store's top
		files[p] = r.Doc
		o.rows[p] = r.Doc
		o.ids = append(o.ids, id)
	}
	for p, b := range extra {
		files[p] = b
	}
	for p, b := range files {
		writeFile(t, filepath.Join(seed, filepath.FromSlash(p)), string(b))
	}
	gitT(t, seed, "add", "-A")
	gitT(t, seed, "commit", "--quiet", "-m", "first plain layout")
	gitT(t, seed, "push", "--quiet", o.remote, testBranch)
	return o
}

// TestStoreDirUpgrade (D-ad): a store of the first plain layout
// (lore_store: 3) gets one commit, StoreDirSubject, moving the root
// folder's table folders and _meta.json from the store's top into _store/
// while project folders stay; lore_store becomes 4; an earlier lore's
// README is replaced; an uncommitted hand edit of a root row and a new
// root row moves with it, uncommitted; a second run does nothing; the
// rows read at the same ids.
func TestStoreDirUpgrade(t *testing.T) {
	ctx := context.Background()
	earlier, err := os.ReadFile(filepath.Join("..", "rowfs", "testdata", "readme-earlier.txt"))
	if err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"id":"prj_01a1a5aca7f57659be7320c5f4b70033"}` + "\n")
	src := newFlatRemote(t, []string{".", "acme"}, map[string][]byte{rowfs.MetaFileName: meta, rowfs.ReadmeFile: earlier})
	dir := filepath.Join(t.TempDir(), "stores", "acme", "notes")
	b := openFlat(t, src.remote, dir, "acme")
	if !b.NeedsFlatten() || !NeedsFlattenAt(dir) {
		t.Fatal("a first-plain-layout clone does not need the layout commit")
	}
	rootRow := ""
	for p := range src.rows {
		if !strings.HasPrefix(p, "acme/") {
			rootRow = p
		}
	}
	writeFile(t, filepath.Join(dir, filepath.FromSlash(rootRow)), "edited by hand\n")
	newRow := rowfs.RowPath(memories, newID(t))
	writeFile(t, filepath.Join(dir, filepath.FromSlash(newRow)), "new by hand\n")

	res, err := b.FlattenLayout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit == "" || !res.StoreDir || res.Moved != 3 || !slices.Equal(res.Folders, []string{"."}) {
		t.Fatalf("upgrade: %+v", res)
	}
	if subject := gitT(t, dir, "log", "-1", "--format=%s"); subject != StoreDirSubject {
		t.Fatalf("commit subject %q", subject)
	}
	if got, want := res.Change.Summary(), "3 rows moved to _store/, README refreshed, marker lore_store: 4"; got != want {
		t.Fatalf("change %q, want %q", got, want)
	}
	if n := gitT(t, dir, "rev-list", "--count", "HEAD"); n != "2" {
		t.Fatalf("%s commits, want the seed and one layout commit", n)
	}
	files := gitT(t, dir, "ls-files")
	for _, want := range []string{rowfs.StoreDataDir + "/" + rootRow, rowfs.StoreDataDir + "/" + rowfs.MetaFileName, "acme/memories/", rowfs.ReadmeFile} {
		if !strings.Contains(files, want) {
			t.Errorf("%s not tracked after the upgrade:\n%s", want, files)
		}
	}
	for _, gone := range []string{"\nmemories/", "\n" + rowfs.MetaFileName} {
		if strings.Contains("\n"+files, gone) {
			t.Errorf("%q still tracked at the store's top:\n%s", strings.TrimPrefix(gone, "\n"), files)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "memories")); err == nil {
		t.Error("the emptied memories/ folder is left at the store's top")
	}
	if got := gitT(t, dir, "show", "HEAD:"+rowfs.StoreDataDir+"/"+rootRow); got != strings.TrimSpace(string(src.rows[rootRow])) {
		t.Fatalf("the commit holds the hand edit: %q", got)
	}
	if got := readFile(t, filepath.Join(dir, rowfs.StoreDataDir, filepath.FromSlash(rootRow))); got != "edited by hand\n" {
		t.Fatalf("hand edit lost: %q", got)
	}
	if got := readFile(t, filepath.Join(dir, rowfs.StoreDataDir, filepath.FromSlash(newRow))); got != "new by hand\n" {
		t.Fatalf("new hand row lost: %q", got)
	}
	st := gitT(t, dir, "status", "--porcelain", "--untracked-files=all")
	if !strings.Contains(st, " M "+rowfs.StoreDataDir+"/"+rootRow) && !strings.Contains(st, "M "+rowfs.StoreDataDir+"/"+rootRow) || !strings.Contains(st, "?? "+rowfs.StoreDataDir+"/"+newRow) {
		t.Fatalf("status %q, want the hand edit and the new row left for the pick-up", st)
	}
	if body := gitT(t, dir, "show", "HEAD:"+rowfs.ReadmeFile); body != strings.TrimRight(rowfs.StoreReadme, "\n") {
		t.Fatal("the earlier lore README was not replaced in the layout commit")
	}
	if m, err := rowfs.ReadMarker(dir); err != nil || m.Version != rowfs.MarkerVersion || !strings.Contains(gitT(t, dir, "show", "HEAD:"+rowfs.MarkerFile), "lore_store: 4") {
		t.Fatalf("marker %+v %v", m, err)
	}
	if NeedsFlattenAt(dir) {
		t.Fatal("still needs the layout commit")
	}
	if again, err := b.FlattenLayout(ctx); err != nil || again.Commit != "" {
		t.Fatalf("second run: %+v %v", again, err)
	}
	// the project folder reads as before (the root row holds the
	// unfinished hand edit, left for the pick-up)
	acme := src.ids[1]
	if strings.HasPrefix(rootRow, "memories/"+acme.String()) {
		acme = src.ids[0]
	}
	if got := get(t, openFlat(t, src.remote, dir, "acme"), acme); got.ID != acme {
		t.Fatalf("row %s reads as %s", acme, got.ID)
	}
}

// TestStoreDirUpgradeMarkerOnly: a first-plain-layout store with no root
// rows still gets the commit that sets lore_store: 4 (so an older lore
// refuses it instead of writing root rows to the top), and a second run
// makes no commit, also when the pending mark of a cut-short run is left.
func TestStoreDirUpgradeMarkerOnly(t *testing.T) {
	src := newFlatRemote(t, []string{"acme"}, nil)
	dir := filepath.Join(t.TempDir(), "stores", "acme", "notes")
	b := openFlat(t, src.remote, dir, "acme")
	res, err := b.FlattenLayout(context.Background())
	if err != nil || res.Commit == "" || res.Moved != 0 {
		t.Fatalf("upgrade: %+v %v", res, err)
	}
	// the commit only touched the README and the marker: the line names
	// them, with no "0 files"
	if got := res.Change.Summary(); strings.Contains(got, "0 ") || !strings.Contains(got, "marker lore_store: 4") || !strings.Contains(got, "README") {
		t.Fatalf("change %q, want the README and the marker named and no zero count", got)
	}
	if !strings.Contains(gitT(t, dir, "show", "HEAD:"+rowfs.MarkerFile), "lore_store: 4") {
		t.Fatal("marker not set to 4")
	}
	writeFile(t, filepath.Join(StateDirOf(dir), flattenPendingFile), "")
	b2 := openFlat(t, src.remote, dir, "acme")
	if again, err := b2.FlattenLayout(context.Background()); err != nil || again.Commit != "" {
		t.Fatalf("second run: %+v %v", again, err)
	}
	if NeedsFlattenAt(dir) {
		t.Fatal("the pending mark is left")
	}
}

// TestStoreDirPath maps the first plain layout's root files into _store/.
func TestStoreDirPath(t *testing.T) {
	id := "mem_01a1a5aca7f57659be7320c5f4b70033"
	for in, want := range map[string]string{
		"memories/" + id + ".json":        "_store/memories/" + id + ".json",
		"_meta.json":                      "_store/_meta.json",
		"_purged.json":                    "_store/_purged.json",
		"acme/memories/" + id + ".json":   "",
		"_store/memories/" + id + ".json": "",
		"memories/notes.txt":              "",
		"README.md":                       "",
		"lore-store.yaml":                 "",
	} {
		got, ok := StoreDirPath(in)
		if ok != (want != "") || got != want {
			t.Errorf("StoreDirPath(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
}
