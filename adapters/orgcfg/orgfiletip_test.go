package orgcfg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrgFileTipDiverged: a store clone whose local commits (held for lore
// sync, C5) touch no org file reads lore-org.yaml from the fetched tip, so
// an admin's pushed own:/repos: patterns are not copied back to the old
// ones; a local change to the org file itself still wins.
func TestOrgFileTipDiverged(t *testing.T) {
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
	put := func(dir, rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		g(dir, "add", "-A")
		g(dir, "commit", "-q", "-m", rel)
	}
	bare := filepath.Join(root, "store.git")
	g(root, "init", "-q", "--bare", "-b", "main", bare)
	admin, mine := filepath.Join(root, "admin"), filepath.Join(root, "mine")
	g(root, "clone", "-q", bare, admin)
	put(admin, storeMarkerFile, "id: sto_1\n")
	put(admin, "org/"+OrgFile, "name: acme\n")
	g(admin, "push", "-q", "origin", "HEAD:main")
	g(root, "clone", "-q", bare, mine)

	// the admin pushes own: patterns; this clone holds a folder commit
	put(admin, "org/"+OrgFile, "name: acme\nown:\n  - github.com/acme/*\n")
	g(admin, "push", "-q", "origin", "HEAD:main")
	put(mine, "core/_meta.json", "{}\n")
	g(mine, "fetch", "-q", "origin")
	track := g(mine, "rev-parse", "refs/remotes/origin/main")
	if tip, ok := orgFileTip(ctx, mine, "main"); !ok || tip != track {
		t.Errorf("diverged, no local org file change: tip %s, want the fetched %s", tip, track)
	}

	// a local edit of the org file itself is kept
	put(mine, "org/"+OrgFile, "name: acme\nrepos:\n  - github.com/client/*\n")
	local := g(mine, "rev-parse", "HEAD")
	if tip, ok := orgFileTip(ctx, mine, "main"); !ok || tip != local {
		t.Errorf("diverged, org file changed here: tip %s, want the local %s", tip, local)
	}
}
