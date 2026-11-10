//go:build userscen

package userscen

import (
	"strings"
	"testing"
)

// TestSyncFixProjects_TwoBootstrapsSameMount: two people each run `lore
// init` in their own branch of one repo, so each branch holds a project row
// and a repos row for the same mount ("app"). After the merge, `lore sync
// fix-projects` re-points the dropped project's repos row onto the kept
// project; that lands two repos rows on one natural key, which is the
// duplicate the merge surfaces, not a rename. The fold must finish (it used
// to stop half way with E_NAME_TAKEN once edits checked natural keys) and
// leave one project.
func TestSyncFixProjects_TwoBootstrapsSameMount(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	app := w.Remote("github.com/client2/app")
	alice := w.Human("alice", "alice@agency.example").Clone(app, "app")
	alice.WriteFile("README.md", "app\n")
	alice.CommitAll("readme")
	alice.AdminPush("origin", "main")
	bob := w.Human("bob", "bob@agency.example").Clone(app, "app")

	alice.Git("checkout", "-q", "-b", "alice-setup")
	alice.MustLore("init", "--non-interactive", "--name=app")
	alice.CommitAll("lore: init (alice)")
	bob.Git("checkout", "-q", "-b", "bob-setup")
	bob.MustLore("init", "--non-interactive", "--name=app")
	bob.CommitAll("lore: init (bob)")
	bob.AdminPush("-u", "origin", "bob-setup")

	alice.Git("checkout", "-q", "main")
	alice.Git("merge", "-q", "alice-setup")
	alice.Git("fetch", "-q", "origin")
	alice.TryGit("merge", "--no-edit", "origin/bob-setup")

	if n := strings.Count(alice.MustLore("project", "list").Stdout, "prj_"); n != 2 {
		t.Fatalf("after the merge want two project rows, got %d", n)
	}
	alice.MustLore("sync", "fix-projects").Says(t, "kept prj_")
	if n := strings.Count(alice.MustLore("project", "list").Stdout, "prj_"); n != 1 {
		t.Fatalf("fix-projects must leave one project, got %d", n)
	}
	// Running it again finds nothing to fix.
	alice.MustLore("sync", "fix-projects").Says(t, "nothing to fix")
}
