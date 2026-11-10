//go:build userscen

package userscen

// sync_land_test.go — landing in a fresh clone that has not committed its
// .lore folder yet: closing tasks lands them on origin/main, and the rows
// the person has only in the work tree (project, repo, actor, policy) must
// stay readable, so the next write still finds its project.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// A person runs lore init in a clone whose origin already has main and,
// before committing .lore, cancels two tasks (each cancel lands finished
// rows on main). The project must still be found by the next task add,
// project list and task list.
func TestSyncLand_CancelBeforeFirstCommitKeepsTheProject(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	app := w.Remote(syncSlugApp)
	p := w.Human("alice", "alice@example.com")
	p.WriteConfig(th.Config{Live: th.FreshFetch()})
	c := p.Clone(app, "app")
	c.Git("commit", "-q", "--allow-empty", "-m", "init")
	c.AdminPush("origin", "main")
	c.MustLore("init", "--non-interactive", "--name=app")
	tl := th.DecodeJSON[syncShowOut](t, c.MustLore("tasklist", "add", "--title", "Inbox", "--body", "default list", "--json")).Data.ID
	for _, title := range []string{"a", "b"} {
		id := th.DecodeJSON[syncShowOut](t, c.MustLore("task", "add", title, "--tasklist", tl, "--json")).Data.ID
		c.MustLore("task", "cancel", id)
	}
	c.MustLore("task", "add", "c", "--tasklist", tl).Never(t, "E_NOT_FOUND")
	c.MustLore("project", "list").Says(t, "app")
	if !syncHas(syncTitles(t, c), "c") {
		t.Fatal("the task added after the landings must be listed")
	}
}

// A person with .lore committed and pushed (live on, the default kinds)
// adds a tag, then attaches it to three rows and comments on each: the
// comments are live, a land carries the tag (and project) to main, and
// they leave live once main is fetched. This checkout never pulled, so the
// tag must still be listed and attachable (read through origin/main).
func TestSyncLand_TagCarriedByLiveCommentsStaysReadable(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	app := w.Remote(syncSlugApp)
	p := w.Human("alice", "alice@example.com")
	p.WriteConfig(th.Config{Live: th.FreshFetch()})
	c := p.Clone(app, "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	c.CommitAll("lore")
	c.AdminPush("origin", "main")
	c.MustLore("tag", "add", "--name", "ttag")
	adds := []struct{ table, noun string }{
		{"architecture_notes", "architecturenote"},
		{"decisions", "decision"},
		{"hotfixes", "hotfix"},
		{"incidents", "incident"},
	}
	landed := false
	for _, a := range adds {
		args := []string{a.noun, "add", "--title", a.noun + " title", "--body", a.noun + " body", "--json"}
		if a.noun == "hotfix" {
			args = append(args, "--severity", "high")
		}
		id := trashID(t, c.MustLore(args...))
		c.MustLore("tag", "attach", "--on-table", a.table, "--on-id", id, "--tag", "ttag").Never(t, "E_NOT_FOUND")
		out := c.MustLore("comment", "add", "--on-table", a.table, "--on-id", id, "--body", "a comment").Never(t, "E_NOT_FOUND")
		landed = landed || strings.Contains(out.Out(), "landed on main")
		c.MustLore("tag", "list").Says(t, "ttag")
	}
	c.MustLore("project", "list").Says(t, "app")
	if !landed {
		t.Fatal("no comment add landed on main: the scenario did not reach the land it tests")
	}
}
