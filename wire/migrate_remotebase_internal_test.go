package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
)

// TestMigrateOwnChangeLeavesOutAPull: devco U-2. A store clone that took
// in a teammate's push while lore migrate run ran moved its tip and its
// data folder, and the run reported both as its own: "1 files upgraded,
// store commit <the teammate's>, pushed". Only what the run itself
// committed counts; with no commit of its own the place has no change.
func TestMigrateOwnChangeLeavesOutAPull(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=x", "GIT_AUTHOR_EMAIL=x@example.test", "GIT_COMMITTER_NAME=x", "GIT_COMMITTER_EMAIL=x@example.test")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(dir, rel, body string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		run(dir, "add", "-A")
		run(dir, "commit", "-q", "-m", rel)
	}
	remote := filepath.Join(root, "remote.git")
	run(root, "init", "-q", "--bare", "-b", "main", remote)
	mine, theirs := filepath.Join(root, "mine"), filepath.Join(root, "theirs")
	run(root, "clone", "-q", remote, mine)
	write(mine, "org/memories/mem_a.json", "{}\n")
	run(mine, "push", "-q", "origin", "HEAD:main")
	run(mine, "branch", "-q", "--set-upstream-to=origin/main")
	run(root, "clone", "-q", remote, theirs)
	before := run(mine, "rev-parse", "HEAD")

	// the teammate pushes; the run's clone pulls it in
	write(theirs, "org/memories/mem_b.json", "{}\n")
	run(theirs, "push", "-q", "origin", "HEAD:main")
	run(mine, "pull", "-q", "--ff-only")
	snaps, err := git.NewSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	p := journalPlace{Clone: true, Dir: mine, Data: "org", TipBefore: before, TipAfter: run(mine, "rev-parse", "HEAD")}
	p.RemoteBase = remoteBase(ctx, snaps, p, p.TipAfter)
	pulled := []git.TreeChange{{Path: "memories/mem_b.json", NewBlob: "b"}}
	diff, err := ownChanges(ctx, snaps, p, pulled)
	if err != nil {
		t.Fatal(err)
	}
	if p.ownTip() || len(diff) != 0 {
		t.Errorf("a pull alone: ownTip %v, own changes %+v; want none (base %q)", p.ownTip(), diff, p.RemoteBase)
	}

	// the run's own commit on top counts, the pulled file still does not
	write(mine, "org/memories/mem_a.json", "{\"_v\":2}\n")
	p.TipAfter = run(mine, "rev-parse", "HEAD")
	p.RemoteBase = remoteBase(ctx, snaps, p, p.TipAfter)
	diff, err = ownChanges(ctx, snaps, p, append(pulled, git.TreeChange{Path: "memories/mem_a.json", OldBlob: "a", NewBlob: "a2"}))
	if err != nil {
		t.Fatal(err)
	}
	if !p.ownTip() || len(diff) != 1 || diff[0].Path != "memories/mem_a.json" {
		t.Errorf("own commit after a pull: ownTip %v, own changes %+v; want only memories/mem_a.json", p.ownTip(), diff)
	}
}
