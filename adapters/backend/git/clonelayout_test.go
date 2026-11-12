package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// openAt makes a real clone at <home>/stores/<rel> and closes it.
func openAt(t *testing.T, home, rel, remote string) ClonePaths {
	t.Helper()
	dir := filepath.Join(home, StoresDir, filepath.FromSlash(rel))
	b, err := OpenClone(context.Background(), remote, dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	return PathsOf(dir)
}

// scanStates scans home and returns each clone by its Rel.
func scanStates(t *testing.T, home string) map[string]CloneFound {
	t.Helper()
	found, err := ScanClones(home)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]CloneFound{}
	for _, f := range found {
		if _, dup := out[f.Rel]; dup {
			t.Fatalf("clone %s listed twice", f.Rel)
		}
		out[f.Rel] = f
	}
	return out
}

// TestPathsOfIsOpenClonesLayout: a real open makes one plain git clone at
// the folder (a .git folder inside, the store's files checked out beside
// it), the lock and the fetch stamp in .git/lore, and nothing else: no
// work/ folder, no bare twin, no .git pointer file, no lock in the tree.
func TestPathsOfIsOpenClonesLayout(t *testing.T) {
	home, remote := t.TempDir(), newRemote(t, true)
	p := openAt(t, home, "owner/notes", remote)
	if !isDir(p.GitDir) {
		t.Fatalf("%s is not a folder", p.GitDir)
	}
	if !isFile(p.Lock) || p.Lock != filepath.Join(p.Dir, ".git", "lore", "lock") {
		t.Fatalf("lock %s missing or not in .git/lore", p.Lock)
	}
	if !isFile(filepath.Join(p.StateDir, FetchStampFile)) {
		t.Fatal("no fetch stamp in .git/lore after the first open")
	}
	if !isFile(filepath.Join(p.Dir, rowfs.MarkerFile)) {
		t.Fatal("the store's files are not checked out in the clone folder")
	}
	for _, gone := range []string{filepath.Join(p.Dir, "work"), p.Dir + ".git", filepath.Join(p.Dir, rowfs.LoreDir, rowfs.LockFileName)} {
		if _, err := os.Lstat(gone); err == nil {
			t.Fatalf("%s exists: the old layout is back", gone)
		}
	}
	if bare := strings.TrimSpace(gitT(t, p.Dir, "rev-parse", "--is-bare-repository")); bare != "false" {
		t.Fatalf("the clone is bare: %s", bare)
	}
	if st := gitT(t, p.Dir, "status", "--porcelain"); strings.TrimSpace(st) != "" {
		t.Fatalf("a fresh clone is not clean: %q", st)
	}
	if up := strings.TrimSpace(gitT(t, p.Dir, "rev-parse", "--abbrev-ref", "@{upstream}")); up != RemoteName+"/"+testBranch {
		t.Fatalf("upstream %q: a plain git pull would not work", up)
	}
	// a first open that is a read sets nothing up for plain git (owner
	// bug 6): the row attributes come with the first write
	if attrs, err := os.ReadFile(filepath.Join(p.GitDir, "info", "attributes")); err == nil && strings.Contains(string(attrs), "*.json") {
		t.Fatalf("a read's first open wrote row attributes: %q", attrs)
	}
	got := scanStates(t, home)
	if len(got) != 1 || got["owner/notes"].State != CloneWhole {
		t.Fatalf("scan of one whole clone: %+v", got)
	}
}

// TestScanClonesStates: a whole clone, a pinned-branch clone, a first
// clone cut short before checkout, a clone that lost its git data and a
// recorded own clone whose folder is gone, each read as its own state; an
// owner folder holding nothing is no clone.
func TestScanClonesStates(t *testing.T) {
	home, remote := t.TempDir(), newRemote(t, true)
	openAt(t, home, "owner/whole", remote)
	openAt(t, home, "owner/whole@feature/x", remote)
	cut := openAt(t, home, "owner/cut", remote)
	gitT(t, cut.Dir, "symbolic-ref", "HEAD", headsPrefix+pendingBranch)
	broken := openAt(t, home, "owner/broken", remote)
	if err := os.Remove(filepath.Join(broken.GitDir, headFileName)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, StoresDir, "empty-owner"), 0o700); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "mine")
	if err := RecordClone(home, "host.example/owner/mine", CloneEntry{Path: gone, Why: WhyOwn}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got := scanStates(t, home)
	want := map[string]CloneState{
		"owner/whole":             CloneWhole,
		"owner/whole@feature/x":   CloneWhole,
		"owner/cut":               CloneNotCheckedOut,
		"owner/broken":            CloneNoGitData,
		"host.example/owner/mine": CloneMissing,
	}
	if len(got) != len(want) {
		t.Fatalf("scan found %d clones, want %d: %+v", len(got), len(want), got)
	}
	for rel, st := range want {
		if got[rel].State != st {
			t.Errorf("%s: state %q, want %q", rel, got[rel].State, st)
		}
	}
	if !got["host.example/owner/mine"].Own {
		t.Error("the recorded own clone is not marked own")
	}
	if got["owner/whole"].Origin != remote {
		t.Errorf("origin %q, want %q", got["owner/whole"].Origin, remote)
	}
	// a cut-short clone is finished by the next open
	b, err := OpenClone(t.Context(), remote, cut.Dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Close()
	if b.Branch() != testBranch || StateOf("owner/cut", cut.Dir).State != CloneWhole {
		t.Fatalf("the cut-short clone was not finished: branch %q", b.Branch())
	}
}

// TestScanClonesReadsOnly: no stores folder is no clones, and a scan
// leaves every folder it saw in place.
func TestScanClonesReadsOnly(t *testing.T) {
	home := t.TempDir()
	if got, err := ScanClones(home); err != nil || len(got) != 0 {
		t.Fatalf("empty home: %+v %v", got, err)
	}
	p := openAt(t, home, "owner/notes", newRemote(t, true))
	if err := os.Remove(filepath.Join(p.GitDir, headFileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanClones(home); err != nil {
		t.Fatal(err)
	}
	if !isFile(filepath.Join(p.Dir, rowfs.MarkerFile)) {
		t.Fatal("a scan removed a clone's files")
	}
}

// TestCloneDirCollision (D-a, D-b): two stores whose paths agree on
// different hosts never share a clone: the first takes stores/<owner>/<repo>,
// the second stores/<owner>/<repo>~2, recorded in state/store-clones.json;
// every later open of either finds its own folder again, also with the
// record lost.
func TestCloneDirCollision(t *testing.T) {
	home := t.TempDir()
	a := mustRemote(t, "git@alpha.example:acme/notes.git")
	b := mustRemote(t, "https://beta.example/acme/notes")
	dirA, own, err := CloneDirFor(home, a, "")
	if err != nil || own || dirA != filepath.Join(home, StoresDir, "acme", "notes") {
		t.Fatalf("first store: %s %v %v", dirA, own, err)
	}
	fakeClone(t, dirA, "git@alpha.example:acme/notes.git")
	dirB, _, err := CloneDirFor(home, b, "")
	if err != nil || dirB != dirA+"~2" {
		t.Fatalf("second store: %s %v, want %s~2", dirB, err, dirA)
	}
	rec := ReadCloneRecord(home).Clones[CloneKey(b, "")]
	if rec.Path != dirB || rec.Why != WhyCollision {
		t.Fatalf("the collision is not recorded: %+v", rec)
	}
	fakeClone(t, dirB, "https://beta.example/acme/notes")
	if again, _, _ := CloneDirFor(home, a, ""); again != dirA {
		t.Fatalf("the first store moved to %s", again)
	}
	if err = os.Remove(CloneRecordPath(home)); err != nil {
		t.Fatal(err)
	}
	if again, _, _ := CloneDirFor(home, b, ""); again != dirB {
		t.Fatalf("without the record the second store went to %s, want %s", again, dirB)
	}
	// a pinned branch is its own folder, and collides the same way
	if p, _, _ := CloneDirFor(home, a, "staging"); p != dirA+"@staging" {
		t.Fatalf("pinned clone %s", p)
	}
}

// TestCloneDirRecordedOwn: a recorded own clone wins over the default
// path, reports own, and ForgetCloneRecord goes back to the default.
func TestCloneDirRecordedOwn(t *testing.T) {
	home, mine := t.TempDir(), filepath.Join(t.TempDir(), "my-notes")
	r := mustRemote(t, "git@host.example:acme/notes")
	if err := RecordClone(home, CloneKey(r, ""), CloneEntry{Path: mine, Why: WhyOwn}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if d, own, err := CloneDirFor(home, r, ""); err != nil || !own || d != mine {
		t.Fatalf("recorded own clone: %s %v %v", d, own, err)
	}
	if err := ForgetCloneRecord(home, CloneKey(r, "")); err != nil {
		t.Fatal(err)
	}
	if d, own, _ := CloneDirFor(home, r, ""); own || d != filepath.Join(home, StoresDir, "acme", "notes") {
		t.Fatalf("after forget: %s %v", d, own)
	}
}

// TestOpenCloneRefusesForeignFolder: lore never clones into a folder that
// holds a person's files, nor into one whose .git is a pointer file; both
// errors name the folder and the next step, and nothing is written.
func TestOpenCloneRefusesForeignFolder(t *testing.T) {
	remote := newRemote(t, true)
	dir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "todo.txt"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenClone(t.Context(), remote, dir, rowfs.FolderRoot, store.OpenOptions{})
	if !errs.Has(err, errs.BadPath) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("a folder of files: %v", err)
	}
	if _, serr := os.Lstat(filepath.Join(dir, ".git")); serr == nil {
		t.Fatal("a refused open wrote .git into the person's folder")
	}
	if err = os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenClone(t.Context(), remote, dir, rowfs.FolderRoot, store.OpenOptions{}); !errs.Has(err, errs.BadPath) {
		t.Fatalf("a .git pointer file: %v", err)
	}
}

// mustRemote parses a store url.
func mustRemote(t *testing.T, raw string) Remote {
	t.Helper()
	r, err := RemoteOfRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// fakeClone makes dir look like a clone of origin (git init + origin).
func fakeClone(t *testing.T, dir, origin string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "--quiet", ".")
	gitT(t, dir, "remote", "add", RemoteName, origin)
}

// TestScanClonesOwnerMe (owner item 17): a git owner literally named "me"
// (stores/me/<repo>) is an ordinary owner folder, and the personal folder
// store stores/_me beside it is never walked as one, even with a stray
// .git-holding folder inside.
func TestScanClonesOwnerMe(t *testing.T) {
	home := t.TempDir()
	openAt(t, home, "me/notes", newRemote(t, true))
	me := filepath.Join(home, StoresDir, PersonalFolder)
	if err := os.MkdirAll(filepath.Join(me, "sub", gitDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(me, rowfs.MarkerFile), []byte("store_id: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := scanStates(t, home)
	if len(got) != 1 || got["me/notes"].State != CloneWhole {
		t.Fatalf("scan = %+v, want only the clone me/notes", got)
	}
}
