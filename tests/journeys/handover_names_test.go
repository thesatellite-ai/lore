//go:build journeys && !windows

package journeys

// handover_names_test.go — after a hand-over, people are named (devco
// N-2, N-11). Lee runs the org with Ann (joined) and Bob and Li (on the
// roster); `lore member list` lines up the CJK name. Bob leads a task Ann
// and Bob watch. Bob leaves: the offboard preview hands his led task over
// with one `lore task reassign --from @bob` line, and Lee runs it. Then
// the task history and Ann's inbox name Bob and Lee (never act_ ids), and
// Bob is no longer a watcher.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/textwidth"
)

// Names of the hand-over-names journey (placeholders).
const (
	hnTask    = "rotate the staging keys"
	hnBob     = "bob"
	hnLi      = "li"
	hnLiName  = "李小龙"
	hnLiEmail = "li@example.test"
	hnLine    = "lore task reassign --from @bob --assign @<someone> --yes"
)

var hnTaskID = regexp.MustCompile(`tsk_[0-9a-f]{32}`)

func TestJourneyHandoverNamesPeople(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	var task string

	w.Step("day1/lee-starts-the-org", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "init").OK(t)
		tt.lore(tlLee, "org", "link", tlOrg, "--project", tlProject, "--yes").OK(t)
		tt.lore(tlLee, "member", "add", "--email", tlAnn.Email, "--name", tlAnn.Name, "--handle", "ann", "--yes").OK(t)
		tt.lore(tlLee, "member", "add", "--email", tlBob.Email, "--name", tlBob.Name, "--handle", hnBob, "--yes").OK(t)
		tt.lore(tlLee, "member", "add", "--email", hnLiEmail, "--name", hnLiName, "--handle", hnLi, "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
	})

	w.Step("day1/member-list-lines-up-a-cjk-name", func() {
		out := tt.lore(tlLee, "member", "list").OK(t).Stdout
		at := -1
		for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			i := strings.Index(l, "  @")
			if i < 0 {
				continue
			}
			if c := textwidth.Width(l[:i]); at < 0 {
				at = c
			} else if c != at {
				t.Fatalf("N-2: the @handle column starts at cell %d and %d:\n%s", at, c, out)
			}
		}
		if at < 0 || !strings.Contains(out, hnLiName) {
			t.Fatalf("member list:\n%s", out)
		}
	})

	w.Step("day1/ann-joins", func() {
		ann := tt.person(tlAnn)
		dir := tt.repo[tlAnn.Email].Dir
		tlFirstSteps(t, ann, dir, tlTeamSetup, "member", tlAnn)
		tlApply(t, ann, dir, tlTeamSetup, "join", map[string]any{"invite": tt.store.SSHURL()})
		tlLastSteps(t, ann, dir, tlTeamSetup, []string{tlFolders})
		tt.lore(tlAnn, "init").OK(t)
	})

	w.Step("week1/bob-leads-a-task-ann-and-bob-watch", func() {
		add := tt.lore(tlLee, "task", "add", "--title", hnTask, "--assign", "@"+hnBob).OK(t)
		task = hnTaskID.FindString(add.Out())
		if task == "" {
			t.Fatalf("no task id in:\n%s", add)
		}
		tt.lore(tlLee, "task", "edit", "--id", task, "--add-watcher", "@ann", "--add-watcher", "@"+hnBob).OK(t)
	})

	w.Step("week3/bob-leaves-and-lee-hands-his-task-over", func() {
		tt.lore(tlLee, "actor", "offboard", "--actor", "@"+hnBob, "--reason", "moved on").Fails(t, 26, hnLine).Never(t, "task edit --id "+task)
		tt.lore(tlLee, "actor", "offboard", "--actor", "@"+hnBob, "--reason", "moved on", "--yes").OK(t)
		tt.lore(tlLee, "task", "reassign", "--from", "@"+hnBob, "--assign", "@lee", "--yes").OK(t).Says(t, "1 tasks reassigned to @lee")
		tt.lore(tlLee, "task", "history", "--id", task).OK(t).
			Says(t, "lead "+tlBob.Name+" → "+tlLee.Name, "assignees "+tlBob.Name+" → "+tlLee.Name).Never(t, "act_")
		tt.lore(tlLee, "task", "show", "--id", task).OK(t).Says(t, tlAnn.Name).Never(t, tlBob.Name)
		tt.lore(tlLee, "sync").OK(t)
	})

	w.Step("week3/anns-inbox-names-people", func() {
		tt.lore(tlAnn, "sync").OK(t)
		tt.lore(tlAnn, "org", "link", tlOrg, "--project", tlProject, "--yes").OK(t) // her clone: Lee's link is not committed
		tt.lore(tlAnn, "inbox").OK(t).Says(t, task, "assignees @"+hnBob+" → @lee").Never(t, "act_")
	})
}
