package orgcfg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDropNetZeroMove (devco:D-565): a set-url to an address and straight
// back left two unpushed move commits that cancel out, the first carrying
// a path on this machine into the org's history. dropNetZeroMove drops
// such a pair; a pair whose first move was pushed, or one with other work
// on top, keeps every commit.
func TestDropNetZeroMove(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	g := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Pat", "GIT_AUTHOR_EMAIL=pat@example.test", "GIT_COMMITTER_NAME=Pat", "GIT_COMMITTER_EMAIL=pat@example.test", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	put := func(dir, body, msg string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, OrgFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		g(dir, "add", "-A")
		g(dir, "commit", "-q", "--allow-empty", "-m", msg)
	}
	bare := filepath.Join(root, "store.git")
	g(root, "init", "-q", "--bare", "-b", "main", bare)
	clone := filepath.Join(root, "clone")
	g(root, "clone", "-q", bare, clone)
	put(clone, "stores: [git.example.test/acme/lore]\n", "seed")
	g(clone, "push", "-q", "origin", "HEAD:main")
	base := g(clone, "rev-parse", "HEAD")

	// there and back, never pushed: both go
	put(clone, "stores: [/srv/elsewhere/lore]\n", "lore: org acme store moved to /srv/elsewhere/lore")
	put(clone, "stores: [git.example.test/acme/lore]\n", "lore: org acme store moved to git.example.test/acme/lore")
	if !dropNetZeroMove(ctx, clone, "main") {
		t.Fatalf("an unpushed round trip was kept:\n%s", g(clone, "log", "--format=%s", base+"..HEAD"))
	}
	if got := g(clone, "rev-parse", "HEAD"); got != base {
		t.Fatalf("HEAD %s after the drop, want %s", got, base)
	}

	// a note between the moves: a real change, kept
	put(clone, "stores: [/srv/elsewhere/lore]\n", "lore: org acme store moved to /srv/elsewhere/lore")
	put(clone, "stores: [git.example.test/acme/lore]\n", "lore: add memory")
	if dropNetZeroMove(ctx, clone, "main") {
		t.Fatal("a pair that is not two moves was dropped")
	}
	g(clone, "reset", "-q", "--hard", base)

	// the first move was pushed: its undo must reach the remote too
	put(clone, "stores: [/srv/elsewhere/lore]\n", "lore: org acme store moved to /srv/elsewhere/lore")
	g(clone, "push", "-q", "origin", "HEAD:main")
	g(clone, "fetch", "-q", "origin")
	put(clone, "stores: [git.example.test/acme/lore]\n", "lore: org acme store moved to git.example.test/acme/lore")
	if dropNetZeroMove(ctx, clone, "main") {
		t.Fatal("a round trip whose first move was pushed was dropped")
	}
}

// TestIsOrgMoveSubject: only the subject MoveOrgFileStore writes counts.
func TestIsOrgMoveSubject(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]bool{
		"lore: org acme store moved to /srv/x":            true,
		"lore: org acme store moved to ":                  false,
		"lore: org  store moved to /srv/x":                false,
		"lore: add memory":                                false,
		"fix: org acme store moved to git.example.test/x": false,
	} {
		if got := isOrgMoveSubject(s); got != want {
			t.Errorf("isOrgMoveSubject(%q) = %v, want %v", s, got, want)
		}
	}
}
