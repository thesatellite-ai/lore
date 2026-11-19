package host

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFreshRepoSettle (devco D-282): after a set-up command succeeds, a
// .lore/ the run made and left with no file in it goes again (store init
// --name me set nothing up in the repo, yet left an empty .lore/); a .lore/
// holding a file (store add's stores.yaml) stays, and so does a .lore/
// that was there before the run (f.lore == "").
func TestFreshRepoSettle(t *testing.T) {
	top := t.TempDir()
	lore := filepath.Join(top, loreDirName)
	if err := os.MkdirAll(filepath.Join(lore, "state", "x"), 0o750); err != nil {
		t.Fatal(err)
	}
	(&freshRepo{lore: lore}).settle()
	if exists(lore) {
		t.Fatal("an empty .lore/ the run made is still there")
	}

	if err := os.MkdirAll(filepath.Join(lore, "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	stores := filepath.Join(lore, "stores.yaml")
	if err := os.WriteFile(stores, []byte("stores: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	(&freshRepo{lore: lore}).settle()
	if !exists(stores) {
		t.Fatal("settle removed a .lore/ file the command wrote")
	}
	if exists(filepath.Join(lore, "empty")) {
		t.Fatal("an empty folder beside the file is still there")
	}

	if err := os.Remove(stores); err != nil {
		t.Fatal(err)
	}
	(&freshRepo{}).settle() // .lore/ was there before the run
	var nilRepo *freshRepo
	nilRepo.settle()
	if !exists(lore) {
		t.Fatal("settle removed a .lore/ that existed before the run")
	}
}
