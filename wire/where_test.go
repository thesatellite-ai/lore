package wire

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opswhere "github.com/thesatellite-ai/lore/ops/where"
)

// TestWhereListsStoreLayout: `lore where` lists a store clone under its
// short name with the row pattern of the plain store layout (rows right
// under each folder, no .lore/data), the clone's .git/lore files, a folder
// store's .lore-state files, and a recorded own clone; the text form names
// each clone's store.
func TestWhereListsStoreLayout(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	const url = "git@github.com:acme/lore-notes.git"
	writeT(t, filepath.Join(home, ConfigFile), "orgs:\n  - name: acme\n    stores:\n      - url: git@github.com:acme/lore-acme.git\n        org_folder: org\n      - url: "+url+"\n")
	dir, _, err := gitbackend.CloneDirOf(home, url, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", url}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeMkdir(t, filepath.Join(dir, ".git", "lore", rowfs.LockFileName), "")
	me := filepath.Join(home, gitbackend.StoresDir, gitbackend.PersonalFolder)
	writeMkdir(t, filepath.Join(me, rowfs.MarkerFile), "store_id: x\n")
	writeMkdir(t, filepath.Join(me, rowfs.StoreStateDir, rowfs.LockFileName), "")

	a := newTestAppHome(t, t.TempDir(), home)
	out, err := a.Where(background(), opswhere.ShowIn{})
	if err != nil {
		t.Fatal(err)
	}
	find := func(p string) (opswhere.Entry, bool) {
		for _, e := range out.Entries {
			if e.Path == p {
				return e, true
			}
		}
		return opswhere.Entry{}, false
	}
	clone, ok := find(dir)
	if !ok {
		t.Fatalf("the clone %s is not listed: %+v", dir, out.Entries)
	}
	if clone.Store != "lore-notes" || !strings.Contains(clone.What, "store lore-notes (") {
		t.Errorf("clone entry does not name the store by its short name: store %q, what %q", clone.Store, clone.What)
	}
	if strings.Contains(clone.What, rowfs.LoreDir+"/"+rowfs.DataDir) || !strings.Contains(clone.What, filepath.Join(dir, "<folder>", "<table>", "<id>.json")) {
		t.Errorf("clone entry does not give the plain store row layout: %q", clone.What)
	}
	if e, ok := find(filepath.Join(dir, ".git", "lore", rowfs.LockFileName)); !ok || e.Store != "lore-notes" {
		t.Errorf(".git/lore/lock not listed under the store: %+v (found %v)", e, ok)
	}
	if e, ok := find(me); !ok || e.Store != "me" || !strings.Contains(e.What, "your personal store me") {
		t.Errorf("the personal folder store %s is not listed as the personal store: %+v (found %v)", me, e, ok)
	}
	if e, ok := find(filepath.Join(me, rowfs.StoreStateDir, rowfs.LockFileName)); !ok || !strings.Contains(e.What, "lock") {
		t.Errorf("the folder store's %s/lock is not listed: %+v", rowfs.StoreStateDir, e)
	}
	if txt := out.Text(); !strings.Contains(txt, "(store lore-notes)") {
		t.Errorf("text form does not name the clone's store:\n%s", txt)
	}
}

// writeMkdir is writeT that makes the file's folder first.
func writeMkdir(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	writeT(t, path, body)
}
