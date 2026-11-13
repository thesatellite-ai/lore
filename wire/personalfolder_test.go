package wire

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestPersonalFolderDefault (owner item 17): the local personal store is
// <home>/stores/_me, the same folder the scan skips and the setup page
// names, and `lore store init --name me` with no url makes it there.
func TestPersonalFolderDefault(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, "stores", "_me")
	if got := personalFolderDir(home); got != want {
		t.Fatalf("personalFolderDir = %s, want %s", got, want)
	}
	if onboard.LocalStoreFolder != gitbackend.PersonalFolder || personalStoresDir != gitbackend.StoresDir {
		t.Fatalf("onboard.LocalStoreFolder %q / gitbackend.PersonalFolder %q (stores dir %q / %q) differ", onboard.LocalStoreFolder, gitbackend.PersonalFolder, personalStoresDir, gitbackend.StoresDir)
	}
	a := newTestAppHome(t, t.TempDir(), home)
	out, err := a.InitStore(background(), opsstores.InitIn{Name: opsstores.PersonalName, RepoOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.URL != fileuri.URL(want, "") {
		t.Errorf("store init me made %s, want %s", out.URL, fileuri.URL(want, ""))
	}
	if !isFolderStore(want) {
		t.Errorf("no folder store at %s", want)
	}
	if _, err := os.Stat(legacyPersonalFolderDir(home)); !os.IsNotExist(err) {
		t.Errorf("stores/me exists after store init me: %v", err)
	}
}

// writeLegacyPersonal makes a pre-item-17 home: a folder store at
// stores/me named by personal.store and by a setup's personal_store.
func writeLegacyPersonal(t *testing.T, home string) string {
	t.Helper()
	old := legacyPersonalFolderDir(home)
	writeMkdir(t, filepath.Join(old, rowfs.MarkerFile), "store_id: sto_x\n")
	writeMkdir(t, filepath.Join(old, "org", "memory", "m1.json"), "{}\n")
	u := fileuri.URL(old, "")
	body := "personal:\n  store: " + u + "\nsetups:\n  - name: boss\n    kind: solo\n    personal_store: " + u + "\n"
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return old
}

// TestMovePersonalFolder: a legacy stores/me folder store that config.yaml
// names is renamed to stores/_me, every config place follows, one line is
// printed; the second run is a no-op.
func TestMovePersonalFolder(t *testing.T) {
	home := t.TempDir()
	old := writeLegacyPersonal(t, home)
	var w bytes.Buffer
	movePersonalFolder(home, &w)
	dst := personalFolderDir(home)
	if !isFolderStore(dst) {
		t.Fatalf("no folder store at %s after the move", dst)
	}
	if _, err := os.Stat(filepath.Join(dst, "org", "memory", "m1.json")); err != nil {
		t.Errorf("the row did not move: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("%s still exists: %v", old, err)
	}
	cfg, err := os.ReadFile(filepath.Join(home, ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	newURL := fileuri.URL(dst, "")
	if strings.Count(string(cfg), newURL) != 2 || strings.Contains(string(cfg), fileuri.URL(old, "")+"\n") {
		t.Errorf("config.yaml does not name %s in both places:\n%s", newURL, cfg)
	}
	if lines := strings.Count(w.String(), "\n"); lines != 1 || !strings.Contains(w.String(), "moved your personal store folder") {
		t.Errorf("printed %q, want one line about the move", w.String())
	}
	w.Reset()
	movePersonalFolder(home, &w)
	again, _ := os.ReadFile(filepath.Join(home, ConfigFile))
	if w.Len() != 0 || string(again) != string(cfg) || !isFolderStore(dst) {
		t.Errorf("second run was not a no-op: printed %q, config changed %v", w.String(), string(again) != string(cfg))
	}
	// New runs it: an App opened on the moved home finds nothing to do.
	newTestAppHome(t, t.TempDir(), home)
	if !isFolderStore(dst) {
		t.Error("opening an App disturbed the moved store")
	}
}

// TestMovePersonalFolderViaNew: the move runs on the first App of a run.
func TestMovePersonalFolderViaNew(t *testing.T) {
	home := t.TempDir()
	writeLegacyPersonal(t, home)
	newTestAppHome(t, t.TempDir(), home)
	if !isFolderStore(personalFolderDir(home)) {
		t.Fatal("New did not move stores/me to stores/_me")
	}
}

// TestMovePersonalFolderOwnerMe: a git clone of owner "me"
// (stores/me/<repo>) is never touched, coexists with stores/_me, and a
// stores/me folder that config.yaml does not name stays where it is.
func TestMovePersonalFolderOwnerMe(t *testing.T) {
	home := t.TempDir()
	clone := filepath.Join(home, "stores", "me", "notes")
	if err := os.MkdirAll(clone, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "git@github.com:me/notes.git"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = clone
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeMkdir(t, filepath.Join(clone, rowfs.MarkerFile), "store_id: sto_y\n")
	dst := personalFolderDir(home)
	writeMkdir(t, filepath.Join(dst, rowfs.MarkerFile), "store_id: sto_x\n")
	body := "personal:\n  store: " + fileuri.URL(dst, "") + "\n"
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	movePersonalFolder(home, &w)
	if w.Len() != 0 {
		t.Errorf("printed %q for a home with nothing to move", w.String())
	}
	if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
		t.Fatalf("the owner-me clone was touched: %v", err)
	}
	clones, err := gitbackend.ScanClones(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(clones) != 1 || clones[0].Rel != "me/notes" {
		t.Errorf("scan = %+v, want only me/notes", clones)
	}

	// A stores/me folder store config.yaml does not name is not lore's
	// personal store: left alone.
	other := t.TempDir()
	writeMkdir(t, filepath.Join(legacyPersonalFolderDir(other), rowfs.MarkerFile), "store_id: sto_z\n")
	movePersonalFolder(other, &w)
	if !isFolderStore(legacyPersonalFolderDir(other)) || w.Len() != 0 {
		t.Errorf("an unnamed stores/me was moved (printed %q)", w.String())
	}
}

// TestMovePersonalFolderTargetExists: with stores/_me already there the
// move refuses (nothing renamed, config unchanged) and says why.
func TestMovePersonalFolderTargetExists(t *testing.T) {
	home := t.TempDir()
	old := writeLegacyPersonal(t, home)
	writeMkdir(t, filepath.Join(personalFolderDir(home), "keep.txt"), "x")
	before, _ := os.ReadFile(filepath.Join(home, ConfigFile))
	var w bytes.Buffer
	movePersonalFolder(home, &w)
	after, _ := os.ReadFile(filepath.Join(home, ConfigFile))
	if !isFolderStore(old) || string(before) != string(after) || !strings.Contains(w.String(), "already exists") {
		t.Errorf("target exists: moved or config changed (printed %q)", w.String())
	}
}
