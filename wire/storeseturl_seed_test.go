package wire

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// seedWorld is a repo whose person has a local personal folder store
// holding one note, and the hosts folder of the fake git host.
func seedWorld(t *testing.T) (hosts, home, repo, note string) {
	t.Helper()
	hosts = histGitWorld(t)
	home = t.TempDir()
	repo = gitRepo(t)
	writeStoresYAML(t, repo, "stores: []\n")
	a := openWith(t, repo, Options{Home: home})
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpInit, `{"name":"me"}`, true); err != nil {
		t.Fatalf("init me: %v", err)
	}
	_ = a.Close() // the next command, as on the command line
	a = openWith(t, repo, Options{Home: home})
	note = histAddMemory(t, a, "me", "a private note")
	_ = a.Close()
	return hosts, home, repo, note
}

// setURLMe runs store set-url --name me --url u in a fresh App.
func setURLMe(t *testing.T, repo, home, u string) (opsstores.SetURLOut, error) {
	t.Helper()
	a := openWith(t, repo, Options{Home: home})
	defer func() { _ = a.Close() }()
	res, err := run(t, a, opsstores.OpSetURL, `{"name":"me","url":"`+u+`"}`, true)
	var out opsstores.SetURLOut
	if err == nil {
		if jerr := json.Unmarshal([]byte(res), &out); jerr != nil {
			t.Fatal(jerr)
		}
	}
	return out, err
}

// TestSetStoreURLSeedsEmptyRepoFromPersonalFolder (devco N-13): the way
// to keep a local personal store off one laptop is set-url to an empty
// private repo, which copies the folder (not its lock) into lore's clone
// and pushes nothing (owner rule 1), then lore sync, which pushes it; the
// store keeps its id and later notes go to the repo too.
// The folder gets a .gitignore for its lock when it is made.
func TestSetStoreURLSeedsEmptyRepoFromPersonalFolder(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts, home, repo, note := seedWorld(t)
	folderDir := personalFolderDir(home)
	if b, err := os.ReadFile(filepath.Join(folderDir, ".gitignore")); err != nil || !strings.Contains(string(b), "/"+rowfs.StoreStateDir+"/") {
		t.Fatalf("the personal folder has no .gitignore for its state: %q %v", b, err)
	}
	m, err := rowfs.ReadMarker(folderDir)
	if err != nil {
		t.Fatal(err)
	}
	u, dir := bareStore(t, hosts, "dana/personal")
	out, err := setURLMe(t, repo, home, u)
	if err != nil {
		t.Fatalf("set-url to an empty repo: %v", err)
	}
	if out.ID != m.ID.String() || !strings.Contains(out.Clone, "copied the folder store") || !strings.Contains(out.Clone, "`lore sync` pushes it") {
		t.Fatalf("set-url did not say it copied the folder into the clone: %+v", out)
	}
	// owner rule 1: set-url pushes nothing; the copy waits in the clone
	if refs := gitIn(t, dir, nil, "for-each-ref"); strings.TrimSpace(refs) != "" {
		t.Fatalf("set-url pushed to the remote:\n%s", refs)
	}
	a := openWith(t, repo, Options{Home: home})
	later := histAddMemory(t, a, "me", "written after the move")
	if _, err = a.SyncStores(background(), "me"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	_ = a.Close()
	tree := gitIn(t, dir, nil, "ls-tree", "-r", "--name-only", "main")
	for _, id := range []string{note, later} {
		if !strings.Contains(tree, schema.TableMemories+"/"+id+".json") {
			t.Fatalf("after lore sync the repo lacks %s:\n%s", id, tree)
		}
	}
	if strings.Contains(tree, rowfs.StoreStateDir) {
		t.Fatalf("the folder's lock reached the repo:\n%s", tree)
	}
}

// TestSetStoreURLRetryAfterRefusalAndStaleClone (devco N-13): a refused
// set-url leaves no clone behind, the refusal of an empty repo says it is
// empty, and an empty clone an older lore left (it opens without a fetch)
// does not keep a retry failing once the person pushed the store there.
func TestSetStoreURLRetryAfterRefusalAndStaleClone(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts, home, repo, _ := seedWorld(t)

	// a repo holding code, not a store: refused, and no clone left
	codeURL, codeDir := bareStore(t, hosts, "dana/code")
	work := gitRepo(t)
	if err := os.WriteFile(filepath.Join(work, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, work, nil, "add", "-A")
	gitIn(t, work, nil, "commit", "-qm", "code")
	gitIn(t, work, nil, "push", "-q", codeDir, "HEAD:main")
	if _, err := setURLMe(t, repo, home, codeURL); !errs.Has(err, errs.NotAStore) {
		t.Fatalf("set-url to a code repo: want E_NOT_A_STORE, got %v", err)
	}
	r, err := gitbackend.RemoteOfRaw(codeURL)
	if err != nil {
		t.Fatal(err)
	}
	if cd, _, _ := gitbackend.CloneDirFor(home, r, ""); exists(cd) {
		t.Fatalf("a refused set-url left the clone %s", cd)
	}

	// the stale case: an empty clone of the new address already on the
	// machine, then the store pushed there by hand
	u, dir := bareStore(t, hosts, "dana/personal")
	a := openWith(t, repo, Options{Home: home})
	if id, oerr := a.storeIDAt(background(), attach.Store{Name: "me", URL: u}); oerr != nil || !id.IsZero() {
		t.Fatalf("open the empty repo: %v %v", id, oerr)
	}
	_ = a.Close()
	push := t.TempDir()
	gitIn(t, push, nil, "init", "-q", "-b", "main")
	if err = os.CopyFS(push, os.DirFS(personalFolderDir(home))); err != nil {
		t.Fatal(err)
	}
	gitIn(t, push, nil, "add", "-A")
	gitIn(t, push, nil, "commit", "-qm", "my notes")
	gitIn(t, push, nil, "push", "-q", dir, "HEAD:main")
	if _, err = setURLMe(t, repo, home, u); err != nil {
		t.Fatalf("set-url after the push, with an empty clone left from before: %v", err)
	}
}

// TestNotAStoreAtNamesTheStep (devco N-13): the refusal of an empty repo
// says it is empty and names the push and the retry with real flags; a
// repo holding something else keeps the url check.
func TestNotAStoreAtNamesTheStep(t *testing.T) {
	e := notAStoreAt("kb", "git@h.test:a/kb.git", true)
	if !errs.Has(e, errs.NotAStore) || !strings.Contains(e.Error(), "empty repository") || !strings.Contains(hintText(e), "lore store set-url --name kb --url git@h.test:a/kb.git") {
		t.Fatalf("empty: %v / %s", e, hintText(e))
	}
	if e = notAStoreAt("kb", "git@h.test:a/kb.git", false); strings.Contains(e.Error(), "empty") || !strings.Contains(hintText(e), "--name kb") {
		t.Fatalf("not empty: %v / %s", e, hintText(e))
	}
}

// hintText is the hint of a typed error ("" when none).
func hintText(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}
