package wire

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestSetPersonalStoreFollowed: `lore store init me <git url>` moves the
// one personal store `me`: setups on the default local folder follow
// personal.store; a setup on its own repo or on none keeps it; a url that
// is the local folder itself changes no setup.
func TestSetPersonalStoreFollowed(t *testing.T) {
	home := t.TempDir()
	local := fileuri.URL(personalFolderDir(home), "")
	body := "setups:\n" +
		"  - name: boss\n    kind: solo\n    personal_store: " + local + "\n" +
		"  - name: own\n    kind: solo\n    personal_store: git@github.com:pat/own.git\n" +
		"  - name: off\n    kind: solo\n    personal_store: none\n"
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// the local folder itself: nothing moves
	moved, err := setPersonalStoreFollowed(home, local)
	if err != nil || len(moved) != 0 {
		t.Fatalf("init me at the local folder: moved %v, err %v", moved, err)
	}
	moved, err = setPersonalStoreFollowed(home, "git@github.com:pat/lore-me.git")
	if err != nil || !slices.Equal(moved, []string{"boss"}) {
		t.Fatalf("init me at a git url: moved %v, err %v; want [boss]", moved, err)
	}
	cfg, err := ReadSetupsConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SharedStore != "git@github.com:pat/lore-me.git" {
		t.Errorf("personal.store = %q", cfg.SharedStore)
	}
	want := map[string]string{"boss": onboard.PersonalStoreInherit, "own": "git@github.com:pat/own.git", "off": onboard.PersonalStoreNone}
	for _, e := range cfg.Setups {
		if e.PersonalStore != want[e.Name] {
			t.Errorf("setup %s personal_store = %q, want %q", e.Name, e.PersonalStore, want[e.Name])
		}
	}
}
