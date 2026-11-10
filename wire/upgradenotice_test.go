package wire

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/selfupdate"
	"github.com/thesatellite-ai/lore/model/ids"
)

// TestUpgradeNoticeQuietPaths: through the App hook, CI, a non-terminal
// stderr, --json and upgrade_notice/auto_upgrade both false in
// config.yaml print nothing and never even record a check (no request
// was made). The rules themselves are tested in adapters/selfupdate.
func TestUpgradeNoticeQuietPaths(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{RepoDir: gitRepo(t), Home: home, Actor: actor, Version: "0.1.19"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() }) // test teardown
	t.Setenv(selfupdate.EnvNoUpgradeNotice, "")
	state := filepath.Join(home, "state", selfupdate.UpgradeCheckFile)
	quiet := func(name string, args []string, tty bool) {
		t.Helper()
		var buf bytes.Buffer
		a.UpgradeNotice(ctx, args, tty, &buf)
		if buf.Len() != 0 {
			t.Errorf("%s printed %q", name, buf.String())
		}
		if _, serr := os.Stat(state); serr == nil {
			t.Fatalf("%s made a check (state file written)", name)
		}
	}
	t.Setenv("CI", "true")
	quiet("CI", []string{"task", "list"}, true)
	t.Setenv("CI", "")
	quiet("no tty", []string{"task", "list"}, false)
	quiet("--json", []string{"task", "list", "--json"}, true)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("upgrade_notice: false\nauto_upgrade: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	quiet("both settings off", []string{"task", "list"}, true)
}
