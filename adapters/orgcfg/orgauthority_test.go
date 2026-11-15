package orgcfg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The made-up people of the org file authority tests.
const (
	authAdmin  = "Ada Admin"
	authAdminE = "ada@example.invalid"
	authMember = "Max Member"
	authMembE  = "max@example.invalid"
)

// authCommit writes files (path → body; "" removes it) in dir and commits
// them as name <email>; it returns the commit id.
func authCommit(t *testing.T, dir, name, email string, files map[string]string) string {
	t.Helper()
	ctx := context.Background()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if body == "" {
			if err := os.Remove(full); err != nil {
				t.Fatal(err)
			}
			continue
		}
		writeFile(t, full, body)
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=" + name, "-c", "user.email=" + email, "commit", "-q", "--no-verify", "-m", "change by " + name},
	} {
		if _, err := gitRun(ctx, append([]string{"-C", dir}, args...)...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	sha, err := gitRun(ctx, "-C", dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(sha)
}

// authOrg is a store whose org folder holds an admin (Ada) and a member
// (Max), and lore-org.yaml as Ada wrote it.
func authOrg(t *testing.T) (dir, first string) {
	t.Helper()
	dir = gitRepo(t, "", "")
	authCommit(t, dir, authAdmin, authAdminE, map[string]string{"lore-store.yaml": "id: sto_x\norg_folder: org\n"})
	first = authCommit(t, dir, authAdmin, authAdminE, map[string]string{
		"org/lore-org.yaml": "name: acme\nstores:\n    - url: git@example.invalid:acme/team.git\n      org_folder: org\n",
	})
	authCommit(t, dir, authAdmin, authAdminE, map[string]string{
		"org/actors/act_a.json":        `{"id":"act_a","stable_key":"human:` + authAdminE + `"}`,
		"org/actors/act_m.json":        `{"id":"act_m","stable_key":"human:` + authMembE + `"}`,
		"org/actor_periods/apd_a.json": `{"id":"apd_a","actor_id":"act_a","org_role":"admin"}`,
		"org/actor_periods/apd_m.json": `{"id":"apd_m","actor_id":"act_m","org_role":"member"}`,
	})
	return dir, first
}

// TestOrgFileMemberChangeHeldBack (devco R-7): a member's commit that adds
// a store to lore-org.yaml is held back, named with its commit and author,
// and the accepted version is the admin's; an admin's own edit on top is
// made on the accepted version, never carrying the held change along.
func TestOrgFileMemberChangeHeldBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir, first := authOrg(t)
	bad := authCommit(t, dir, authMember, authMembE, map[string]string{
		"org/lore-org.yaml": "name: acme\nstores:\n    - url: git@example.invalid:outside/notes.git\n    - url: git@example.invalid:acme/team.git\n      org_folder: org\n",
	})
	at, held := acceptedOrgFile(ctx, dir, "HEAD", "org")
	if at != first || len(held) != 1 {
		t.Fatalf("acceptedOrgFile = %s, %v; want the admin's commit %s and one held change", at, held, first)
	}
	h := held[0]
	if !strings.HasPrefix(bad, h.Commit) || h.Author != authMember || h.Email != authMembE || !strings.Contains(h.String(), "not an admin") {
		t.Errorf("held change %q does not name the commit %s and its author", h, bad)
	}
	raw := acceptedOrgRaw(ctx, dir, "HEAD", "org", []byte("tip"))
	if strings.Contains(string(raw), "outside") || !strings.Contains(string(raw), "acme/team") {
		t.Errorf("acceptedOrgRaw = %q, want the admin's version", raw)
	}
	// the admin's own change is accepted
	authCommit(t, dir, authAdmin, authAdminE, map[string]string{
		"org/lore-org.yaml": "name: acme\nstores:\n    - url: git@example.invalid:acme/team.git\n      org_folder: org\n    - url: git@example.invalid:acme/client.git\n",
	})
	if at, held := acceptedOrgFile(ctx, dir, "HEAD", "org"); at == "" || len(held) != 1 {
		t.Errorf("after the admin's edit: %s, %v; want it accepted and the member's change still named", at, held)
	}
}

// TestOrgFileMemberMoveAccepted: a member's commit that changes nothing
// the file says (a layout move, a reformat) is accepted, so lore's own
// moves by any teammate never hold the org file back.
func TestOrgFileMemberMoveAccepted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := gitRepo(t, "", "")
	authCommit(t, dir, authAdmin, authAdminE, map[string]string{
		"org/.lore/lore-org.yaml":      "name: acme\nstores:\n  - url: git@example.invalid:acme/team.git\n",
		"org/actors/act_a.json":        `{"id":"act_a","stable_key":"human:` + authAdminE + `"}`,
		"org/actors/act_m.json":        `{"id":"act_m","stable_key":"human:` + authMembE + `"}`,
		"org/actor_periods/apd_a.json": `{"id":"apd_a","actor_id":"act_a","org_role":"admin"}`,
	})
	authCommit(t, dir, authMember, authMembE, map[string]string{
		"org/.lore/lore-org.yaml": "",
		"org/lore-org.yaml":       "name: acme\nstores:\n    - url: git@example.invalid:acme/team.git\n",
	})
	if at, held := acceptedOrgFile(ctx, dir, "HEAD", "org"); at != "" || len(held) != 0 {
		t.Fatalf("a member's move of the org file: %s, %v; want it accepted", at, held)
	}
}

// TestOrgFileNoAdminRoleCountsCreator: an org made before org roles (no
// stint has org_role admin) counts the creator of the org folder's
// project row as its admin; anyone else's change is still held back.
func TestOrgFileNoAdminRoleCountsCreator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := gitRepo(t, "", "")
	authCommit(t, dir, authAdmin, authAdminE, map[string]string{
		"org/lore-org.yaml":         "name: acme\nstores:\n    - url: git@example.invalid:acme/team.git\n",
		"org/actors/act_a.json":     `{"id":"act_a","stable_key":"human:` + authAdminE + `"}`,
		"org/actors/act_m.json":     `{"id":"act_m","stable_key":"human:` + authMembE + `"}`,
		"org/projects/prj_org.json": `{"id":"prj_org","created_by":"act_a"}`,
	})
	authCommit(t, dir, authMember, authMembE, map[string]string{
		"org/lore-org.yaml": "name: acme\nstores:\n    - url: git@example.invalid:outside/notes.git\n",
	})
	if _, held := acceptedOrgFile(ctx, dir, "HEAD", "org"); len(held) != 1 {
		t.Fatalf("a member's change with no org roles: held %v, want it held back", held)
	}
	authCommit(t, dir, authAdmin, authAdminE, map[string]string{
		"org/lore-org.yaml": "name: acme\nstores:\n    - url: git@example.invalid:acme/team.git\n    - url: git@example.invalid:acme/client.git\n",
	})
	at, _ := acceptedOrgFile(ctx, dir, "HEAD", "org")
	head, _ := gitRun(ctx, "-C", dir, "rev-parse", "HEAD")
	if at != strings.TrimSpace(head) {
		t.Errorf("the creator's change is not accepted: %s", at)
	}
}

// TestOrgFileForgedAdminIsNotAdmin (devco R-5 with R-7): a member who
// first edits his own stint to admin by hand, then changes lore-org.yaml,
// is still held back: who is admin at a commit comes from the same trusted
// roster replay the roster check uses, never from the roster as stored.
func TestOrgFileForgedAdminIsNotAdmin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir, first := authOrg(t)
	authCommit(t, dir, authMember, authMembE, map[string]string{
		"org/actor_periods/apd_m.json": `{"id":"apd_m","actor_id":"act_m","org_role":"admin"}`,
	})
	authCommit(t, dir, authMember, authMembE, map[string]string{
		"org/lore-org.yaml": "name: acme\nstores:\n    - url: git@example.invalid:outside/notes.git\n    - url: git@example.invalid:acme/team.git\n      org_folder: org\n",
	})
	if at, held := acceptedOrgFile(ctx, dir, "HEAD", "org"); at != first || len(held) != 1 {
		t.Fatalf("acceptedOrgFile = %s, %v; want the admin's commit %s and the forged admin's change held", at, held, first)
	}
}
