package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestLiveProjectRootSkipsHomeAndPlainFolders (devco:D-876): a .lore folder
// is a project only inside a git work tree and only when it is not the lore
// home. The lore home's run/ (the read server's lock) in a parent folder
// made every plain folder under it a "project", and `lore sync` there
// failed with E_INTERNAL "git check-ignore ... not a git repository"; a
// dotfiles repo at the person's home made the home itself the project.
// Break-verified: the old walk returns the parent folder in the first two
// cases.
func TestLiveProjectRootSkipsHomeAndPlainFolders(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	mk := func(p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "read.lock"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// a .lore folder with no git work tree around it (another process's
	// lore home in a temp folder)
	plain := t.TempDir()
	mk(filepath.Join(plain, ".lore", "run"))
	sub := filepath.Join(plain, "notes")
	mk(sub)
	if got := liveProjectRoot(sub); got != "" {
		t.Errorf("no git work tree: liveProjectRoot = %q, want none", got)
	}
	if err := (liveSync{h: &liveHost{cfg: LiveConfig{WorkDir: sub}}}).refuseIgnoredData(context.Background()); err != nil {
		t.Errorf("sync's ignored-data check in a plain folder: %v", err)
	}

	// the lore home inside a dotfiles repo at the person's home
	home := t.TempDir()
	gitT(t, home, "init", "-q")
	mk(filepath.Join(home, ".lore", "run"))
	t.Setenv("LORE_HOME", filepath.Join(home, ".lore"))
	notes := filepath.Join(home, "notes")
	mk(notes)
	if got := liveProjectRoot(notes); got != "" {
		t.Errorf("lore home in a dotfiles repo: liveProjectRoot = %q, want none", got)
	}

	// a real project in that repo is still found, from a subfolder too
	mk(filepath.Join(notes, ".lore", "data"))
	deep := filepath.Join(notes, "src")
	mk(deep)
	if got := liveProjectRoot(deep); got != notes {
		t.Errorf("project in a repo: liveProjectRoot = %q, want %q", got, notes)
	}
}
