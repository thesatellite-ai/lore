package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire"
)

// TestInitFailedLeavesGitConfig (devco D-281-initundo): a lore init that
// fails after it wrote _meta.json (here the origin url carries the agent's
// tool session id as a value, so the repo row is refused) leaves the
// clone's .git/config byte for byte as it was: no lore.initedhere key. The
// CLI runs repo.init outside the Runner, whose set-up undo `lore repo init`
// already had.
func TestInitFailedLeavesGitConfig(t *testing.T) {
	t.Parallel()
	const session = "3f2a9c1e-7b4d-4e8a-9c2f-1a2b3c4d5e6f" // placeholder tool session id
	w := newCLIWorld(t).unscoped().claude(session)
	dir := w.root
	w.git(t, dir, "init", "-q", "-b", "main")
	w.git(t, dir, "remote", "add", "origin", "https://example.test/"+session+"/app.git")
	cfg := filepath.Join(dir, ".git", "config")
	before, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	code, out, errOut := w.lore(t, dir, "init", "--name", "app")
	if code == 0 || !strings.Contains(errOut, "session-id") {
		t.Fatalf("lore init with the session id in its origin url: exit %d\n%s\n%s", code, out, errOut)
	}
	after, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the failed init changed .git/config:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if strings.Contains(string(after), strings.TrimPrefix(wire.InitedHereGitKey, "lore.")) {
		t.Fatalf("the failed init left %s set", wire.InitedHereGitKey)
	}
}
