package orgcfg

import (
	"context"
	"strings"
	"testing"
)

// authGit runs git in dir as name <email> and fails t on an error.
func authGit(t *testing.T, dir, name, email string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=" + name, "-c", "user.email=" + email}, args...)
	if _, err := gitRun(context.Background(), full...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

// TestOrgFileEvilMergeHeld (devco:D-999): a member's hand-made merge
// whose lore-org.yaml is neither parent's version (a store slipped in
// while resolving the merge) is held back like a plain commit of the
// same change, and its hint reverts the merge the way git accepts
// (git revert -m 1). Before, the org file history was read with
// --no-merges and the planted store stood. The path-limited walk kept
// for a tip the replay does not reach judges it the same.
func TestOrgFileEvilMergeHeld(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir, first := authOrg(t)
	authGit(t, dir, authAdmin, authAdminE, "switch", "-q", "-c", "side")
	authCommit(t, dir, authAdmin, authAdminE, map[string]string{"notes/side.txt": "side\n"})
	authGit(t, dir, authAdmin, authAdminE, "switch", "-q", "-")
	authCommit(t, dir, authMember, authMembE, map[string]string{"notes/main.txt": "main\n"})
	authGit(t, dir, authMember, authMembE, "merge", "-q", "--no-ff", "--no-commit", "side")
	merge := authCommit(t, dir, authMember, authMembE, map[string]string{
		"org/lore-org.yaml": "name: acme\nstores:\n    - url: git@example.invalid:outside/notes.git\n    - url: git@example.invalid:acme/team.git\n      org_folder: org\n",
	})
	if parents, _ := gitRun(ctx, "-C", dir, "rev-list", "--parents", "-n1", merge); len(strings.Fields(parents)) != 3 {
		t.Fatalf("the fixture's last commit is not a merge: %q", parents)
	}
	check := func(how string, at string, held []HeldOrgChange) {
		t.Helper()
		if at != first || len(held) != 1 {
			t.Fatalf("%s: acceptedOrgFile = %s, %v; want the admin's commit %s and the member's merge held back", how, at, held, first)
		}
		if h := held[0]; !strings.HasPrefix(merge, h.Commit) || h.Email != authMembE || !strings.Contains(h.Why, "git revert -m 1 "+h.Commit) {
			t.Errorf("%s: held change %q does not name the merge %s with a revert git accepts", how, h, merge)
		}
	}
	at, held := acceptedOrgFile(ctx, dir, "HEAD", "org")
	check("from the replay", at, held)
	at, held = acceptedOrgFileLog(ctx, dir, "HEAD", "org")
	check("from the walk", at, held)
}

// TestOrgFileReadsNoHistory (devco:D-999, store growth item 5): with the
// clone's roster replay at HEAD, the org file check walks no history:
// not again at the same HEAD, and not after new commits (they are
// carried into the replay). Before, every read ran a path-limited git log
// of the whole history (each core open). The verdicts stay the same.
func TestOrgFileReadsNoHistory(t *testing.T) {
	ctx := context.Background()
	dir, first := authOrg(t)
	authCommit(t, dir, authMember, authMembE, map[string]string{
		"org/lore-org.yaml": "name: acme\nstores:\n    - url: git@example.invalid:outside/notes.git\n",
	})
	before := orgLogWalks.Load()
	for i := range 3 {
		if at, held := acceptedOrgFile(ctx, dir, "HEAD", "org"); at != first || len(held) != 1 {
			t.Fatalf("read %d: acceptedOrgFile = %s, %v; want the admin's commit and one held change", i, at, held)
		}
	}
	authCommit(t, dir, authAdmin, authAdminE, map[string]string{"notes/later.txt": "later\n"})
	if at, held := acceptedOrgFile(ctx, dir, "HEAD", "org"); at != first || len(held) != 1 {
		t.Fatalf("after a new commit: acceptedOrgFile = %s, %v; want the same verdict", at, held)
	}
	if n := orgLogWalks.Load() - before; n != 0 {
		t.Errorf("the org file check walked the history %d time(s), want 0 (the roster replay lists its changes)", n)
	}
}
