//go:build teame2e

package teame2e

// Issue #4 (B-ERRS2-4 side finding): `lore task split` in a second clone
// of a task whose parent sits in the first clone's on-demand "default"
// task list. Wherever that list reaches the teammate (live with the task,
// or a pushed commit), the split succeeds and the children join the
// parent's list; while it has not reached them (committed on dev1's
// branch, not pushed: F-ADDADD-2 keeps it off live), the refusal names
// the list id and the next step instead of the engine's "create the
// target first, or attach the store" (the wrong advice for a row a
// teammate already made), and after the push and a `git pull` the same
// split succeeds.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// splitListHow is how the parent's default task list travels to dev2.
type splitListHow string

const (
	listLive     splitListHow = "list rides live with the task"
	listPushed   splitListHow = "list committed and pushed, dev2 pulls"
	listUnpushed splitListHow = "list committed on dev1's branch, pushed only later"
)

func TestTaskSplitSecondCloneDefaultList(t *testing.T) {
	for _, how := range []splitListHow{listLive, listPushed, listUnpushed} {
		t.Run(string(how), func(t *testing.T) {
			w := newTeamWorld(t, th.Cmd("task", "split"), th.Flag("no-live", "task", "add"))
			app := w.Remote(slugApp)
			dev1 := w.Human("dev1", "dev1@acme.example")
			dev2 := w.Human("dev2", "dev2@acme.example")
			for _, a := range []*th.Actor{dev1, dev2} {
				a.WriteConfig(th.Config{Live: th.FreshFetch()})
			}
			c1 := dev1.Clone(app, "app")
			c1.MustLore("init", "--non-interactive", "--name=app")
			c1.CommitAll("lore")
			c1.AdminPush("origin", "main")
			c2 := dev2.Clone(app, "app")
			c2.MustLore("task", "list")

			var parent string
			switch how {
			case listLive:
				parent = mustID(t, c1.MustLore("task", "add", "ship the thing", "--commitment", "accepted"), "tsk")
			case listPushed:
				parent = mustID(t, c1.MustLore("task", "add", "ship the thing", "--commitment", "accepted", "--no-live"), "tsk")
				c1.CommitAll("lore: task")
				c1.AdminPush("origin", "main")
				c2.Git("pull", "-q")
			case listUnpushed:
				// the default list lands in the work tree with a --no-live
				// task and is committed but not pushed; the live task then
				// names it, and live leaves it on the branch (F-ADDADD-2)
				mustID(t, c1.MustLore("task", "add", "groundwork", "--commitment", "accepted", "--no-live"), "tsk")
				c1.CommitAll("lore: groundwork")
				parent = mustID(t, c1.MustLore("task", "add", "ship the thing", "--commitment", "accepted"), "tsk")
			}
			list := mustID(t, c1.MustLore("task", "show", parent, "--json"), "tlt")

			if how == listUnpushed {
				r := c2.Lore("task", "split", parent, "first half", "second half")
				if r.Code != th.ExitNotFound || !strings.Contains(r.Out(), "E_NOT_FOUND") {
					t.Fatalf("split before the list reached dev2: want E_NOT_FOUND, got %s", r)
				}
				for _, want := range []string{list, "not in this clone yet", "git pull"} {
					if !strings.Contains(r.Out(), want) {
						t.Fatalf("the refusal must name %q (the list and the next step): %s", want, r)
					}
				}
				if strings.Contains(r.Out(), "create the target first") {
					t.Fatalf("the refusal tells dev2 to create a list dev1 already made: %s", r)
				}
				// task add --parent inherits the list the same way
				r = c2.Lore("task", "add", "one child", "--parent", parent)
				if r.Code != th.ExitNotFound || !strings.Contains(r.Out(), list) || !strings.Contains(r.Out(), "git pull") {
					t.Fatalf("task add --parent before the list reached dev2: want E_NOT_FOUND naming %s and git pull, got %s", list, r)
				}
				c1.AdminPush("origin", "main")
				c2.Git("pull", "-q")
			}

			r := c2.MustLore("task", "split", parent, "first half", "second half")
			if !strings.Contains(r.Out(), "inherited: tasklist "+list) {
				t.Fatalf("the children must join the parent's list %s: %s", list, r)
			}
			kids := idPrefixed("tsk").FindAllString(r.Out(), -1)
			n := 0
			for _, k := range kids {
				if k == parent {
					continue
				}
				n++
				if show := c2.MustLore("task", "show", k, "--json"); !strings.Contains(show.Out(), list) {
					t.Fatalf("child %s is not in the parent's list %s: %s", k, list, show)
				}
			}
			if n != 2 {
				t.Fatalf("want 2 children, got %d in %s", n, r)
			}
		})
	}
}
