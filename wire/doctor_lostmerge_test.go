package wire

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestDoctorLostMergesNamesSquashedTasks (devco O-12): a done task whose
// done commit is on no branch any more (squash-merged with a message that
// dropped its Lore-Task trailer, then the branch deleted) is named once,
// with `lore task merged --id <id>` as the fix; a task whose commit is
// still on a branch, or whose trailer a commit on a branch carries (the
// lazy merged check stamps that one), is not named. The lore sync line
// is checked by TestJourneySquashLostMerge (the real binary's sync host).
func TestDoctorLostMergesNamesSquashedTasks(t *testing.T) {
	repo := gitRepo(t)
	notClient := func(context.Context) (bool, error) { return false, nil }
	a := openWith(t, repo, Options{RepoIsClient: notClient})
	withProject(t, a)
	git := func(args ...string) string { return gitIn(t, repo, b11GitEnv, args...) }
	git("commit", "-q", "--allow-empty", "-m", "base")
	trunk := git("rev-parse", "--abbrev-ref", "HEAD")
	add := func(title string) string {
		out, err := run(t, a, opstask.OpAdd, `{"title":"`+title+`"}`, true)
		if err != nil {
			t.Fatal(err)
		}
		var shown struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &shown); err != nil || shown.Data.ID == "" {
			t.Fatalf("task add: %s (%v)", out, err)
		}
		return shown.Data.ID
	}
	// work commits a change on its own branch and returns the commit;
	// drop deletes the branch afterwards (the squash merge's cleanup).
	work := func(branch, msg string, drop bool) string {
		git("checkout", "-q", "-b", branch)
		git("commit", "-q", "--allow-empty", "-m", msg)
		sha := git("rev-parse", "HEAD")
		git("checkout", "-q", trunk)
		if drop {
			git("branch", "-q", "-D", branch)
		}
		return sha
	}
	done := func(id, sha string) {
		if _, err := run(t, a, opstask.OpDone, `{"id":"`+id+`","commit":"`+sha+`","no_lesson":true}`, true); err != nil {
			t.Fatal(err)
		}
	}
	kept, lost, trailed := add("kept branch"), add("squashed without trailer"), add("squashed with trailer")
	done(kept, work("feat-kept", "kept work", false))
	done(lost, work("feat-lost", "lost work\n\nLore-Task: "+lost, true))
	done(trailed, work("feat-trailed", "trailed work", true))
	git("commit", "-q", "--allow-empty", "-m", "Squash of the lost work")
	git("commit", "-q", "--allow-empty", "-m", "Squash of the trailed work\n\nLore-Task: "+trailed)

	got, err := a.DoctorLostMerges(background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Check != DoctorCheckLostMerge || got[0].Level != DoctorNote ||
		!strings.Contains(got[0].Message, lost) || !strings.Contains(got[0].Fix, "lore task merged --id "+lost) {
		t.Fatalf("want one note naming %s and its fix, got %+v", lost, got)
	}
	// lore task merged stamps merged_at (here the same stamp, without a remote).
	if ok, err := stampMergedRow(background(), engineRows{a: a}, pushingSignals{}, lost, time.Now().UTC()); err != nil || !ok {
		t.Fatalf("stamp: %v %v", ok, err)
	}
	if got, err = a.DoctorLostMerges(background()); err != nil || len(got) != 0 {
		t.Fatalf("after task merged: %+v %v", got, err)
	}
}
