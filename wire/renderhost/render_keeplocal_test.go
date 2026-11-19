package renderhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire"
)

// keepLocalGit runs git in dir for the keep-local tests, failing t on error.
func keepLocalGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Ada Ray", "GIT_AUTHOR_EMAIL=ada@example.test", "GIT_COMMITTER_NAME=Ada Ray", "GIT_COMMITTER_EMAIL=ada@example.test", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// writeKeepLocal writes body to root/rel, creating its folder.
func writeKeepLocal(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestTeammateRenderKeepsRenderLocal (devco N-7, the render side of stories
// A-1): an admin inits a project and pushes it without its render; a
// teammate's `lore render` in their clone writes .lore/LORE.md and
// LORE.full.md. They must not stay untracked and unignored: once the admin
// commits the same files, the teammate's git pull aborts ("untracked
// working tree files would be overwritten by merge"). The teammate's render
// files go to the clone's .git/info/exclude (with one line saying so and
// how to commit them anyway), and the pull then takes the admin's commit.
// The clone that ran lore init keeps them in git status for its commit.
func TestTeammateRenderKeepsRenderLocal(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	remote := filepath.Join(base, "remote.git")
	keepLocalGit(t, base, "init", "-q", "--bare", "-b", "main", remote)
	admin := filepath.Join(base, "admin")
	keepLocalGit(t, base, "clone", "-q", remote, admin)
	writeKeepLocal(t, admin, ".lore/data/_meta.json", `{"_v": 2, "format": 2, "project_id": "prj_01"}`+"\n")
	keepLocalGit(t, admin, "config", "--local", wire.InitedHereGitKey, "true") // what lore init sets
	keepLocalGit(t, admin, "add", "-A")
	keepLocalGit(t, admin, "commit", "-qm", "lore init")
	keepLocalGit(t, admin, "push", "-q", "origin", "HEAD:main")

	mate := filepath.Join(base, "mate")
	keepLocalGit(t, base, "clone", "-q", remote, mate)
	if wire.RenderOwnerHere(ctx, mate) {
		t.Fatal("a teammate's clone counts as the one whose commit brings the first render")
	}
	writeKeepLocal(t, mate, LoreMDRel, "teammate render\n")
	writeKeepLocal(t, mate, LoreFullRel, "teammate full\n")
	h := &Host{}
	var out strings.Builder
	if err := h.keepRenderLocal(ctx, &out, mate, false, LoreMDRel, LoreFullRel); err != nil {
		t.Fatal(err)
	}
	if st := keepLocalGit(t, mate, "status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Fatalf("the teammate's render stays in git status, a pull of the admin's render will abort:\n%s", st)
	}
	if !strings.Contains(out.String(), "git add -f .lore/LORE.md .lore/LORE.full.md") {
		t.Errorf("the render line does not say how to commit the files from this clone: %q", out.String())
	}

	if !wire.RenderOwnerHere(ctx, admin) {
		t.Fatal("the clone that ran lore init does not own the first render")
	}
	writeKeepLocal(t, admin, LoreMDRel, "admin render\n")
	writeKeepLocal(t, admin, LoreFullRel, "admin full\n")
	if err := h.keepRenderLocal(ctx, &out, admin, false, LoreMDRel, LoreFullRel); err != nil {
		t.Fatal(err)
	}
	if st := keepLocalGit(t, admin, "status", "--porcelain", "--untracked-files=all"); !strings.Contains(st, "LORE.md") {
		t.Fatalf("the admin's render was kept out of git status, so no commit would carry it:\n%s", st)
	}
	keepLocalGit(t, admin, "add", "-A")
	keepLocalGit(t, admin, "commit", "-qm", "render")
	keepLocalGit(t, admin, "push", "-q", "origin", "HEAD:main")

	keepLocalGit(t, mate, "pull", "-q", "--no-rebase", "origin", "main")
	if b, err := os.ReadFile(filepath.Join(mate, filepath.FromSlash(LoreMDRel))); err != nil || string(b) != "admin render\n" {
		t.Fatalf("the teammate's pull did not take the admin's render: %q %v", b, err)
	}
}
