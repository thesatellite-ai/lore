//go:build journeys && !windows

package journeys

// argshapes_test.go — a newcomer types lore commands the way they read
// them (A-10, A-11): a value where a command word goes is answered with
// the command that takes it, every --due reads the same date phrases, and
// `task done --commit` says whether the commit is linked to the task.

import (
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// asTaskID matches the id `task add` prints.
var asTaskID = regexp.MustCompile(`tsk_[0-9a-f]+`)

func TestJourneyArgShapes(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kit := w.Persona("Kit Arlo", "kit@shapes.example")
	lap := kit.Laptop()
	app := w.Remote("github.com/shapes-dev/app")
	var repo *teamharness.Clone
	var id string

	w.Step("day1/init-and-a-task", func() {
		repo = lap.Clone(app, "app")
		lap.LoreIn(repo, "init").OK(t)
		r := lap.LoreIn(repo, "task", "add", "--title", "wire the retry", "--commitment", "accepted").OK(t)
		if id = asTaskID.FindString(r.Out()); id == "" {
			t.Fatalf("task add printed no id: %s", r)
		}
	})

	w.Step("day1/a-value-where-a-command-goes", func() {
		lap.LoreIn(repo, "commit", "HEAD").Fails(t, 20, `unknown command "HEAD"`, "lore commit show --id HEAD")
		lap.LoreIn(repo, "task", "tree", id).Fails(t, 20, "lore task show --id "+id)
		lap.LoreIn(repo, "memory", "bogus").Fails(t, 20, `unknown command "bogus"`, "hint: lore memory --help lists its commands").Never(t, "--id bogus")
		lap.LoreIn(repo, "status").Fails(t, 20, "did you mean", "lore migrate status", "lore --help lists its commands") // C-13
	})

	w.Step("day1/reminder-due-tomorrow", func() {
		lap.LoreIn(repo, "reminder", "add", "--message", "check the rollout", "--due", "tomorrow").OK(t).Says(t, `(from "tomorrow")`)
	})

	w.Step("day2/done-with-a-commit-says-if-it-is-linked", func() {
		sha := repo.Git("rev-parse", "--short=7", "HEAD")
		lap.LoreIn(repo, "task", "done", "--id", id, "--commit", sha, "--no-lesson").OK(t).
			Says(t, "not linked to the task; link it with: lore link add --entity "+id+" --commit "+sha)
		lap.LoreIn(repo, "link", "add", "--entity", id, "--commit", sha).OK(t)
		lap.LoreIn(repo, "commit", "show", sha).OK(t).Says(t, "tasks/"+id)
	})
}
