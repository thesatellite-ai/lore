//go:build journeys && !windows

package journeys

// adopt_move_test.go — devco:D-713, D-715, D-716 (owner, moving real repos
// into a store with 0.1.18): moving one repo's notes into a store folder
// took `lore store adopt --remove-source`, `lore org link`, a `lore row
// move` per open (live) row ("two live rows with one id"),
// `LORE_ALLOW_ROW_DROP=1 git commit` (which failed with E_INTERNAL
// "pre-commit: stage .lore/data" once the deletions were staged) and a
// hand cleanup. Now it is one command: `lore store adopt --move` adopts,
// takes the rows off the repo's live ref, removes the row files, links
// the repo to the folder and prints the one git command left; the commit
// goes through lore's own hooks, a teammate who pulls reads every row from
// the folder, once. `--move --commit` makes that commit itself. Lee's repo
// holds 212 rows, 140 of them open tasks on the live ref.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	jAmSlug    = "github.com/acme-co/ledger-app"
	jAmSlug2   = "github.com/acme-co/ledger-two"
	jAmFolder  = "client_ledger"
	jAmFolder2 = "client_ledger_two"
	jAmTasks   = 210 // every third done (lands in .lore/data), the rest open (on the live ref)
	jAmOpen    = jAmTasks - jAmTasks/3
)

// jAmSeed fills the repo with a rule, a memory and jAmTasks imported
// tasks, commits and pushes; it returns an open task's id.
func jAmSeed(t *testing.T, m *Machine, repo *teamharness.Clone, dir string) string {
	t.Helper()
	m.Lore(dir, "init", "--non-interactive", "--name=ledger").OK(t)
	m.Lore(dir, "rule", "add", "--body", "Run the linter before every push", "--severity", "must", "--to", "repo", "--yes").OK(t)
	m.Lore(dir, "memory", "add", "--body", "the ledger cache warms at boot", "--to", "repo").OK(t)
	var b strings.Builder
	b.WriteString("id,title,body,priority,status\n")
	for i := 1; i <= jAmTasks; i++ {
		st := "todo"
		if i%3 == 0 {
			st = "done"
		}
		fmt.Fprintf(&b, "LED-%d,Ledger row %03d,carried over,medium,%s\n", i, i, st)
	}
	repo.WriteFile("export.csv", b.String())
	m.Lore(dir, "task", "import", "--file", "export.csv", "--to", "repo", "--yes").OK(t)
	repo.Git("rm", "-q", "--cached", "--ignore-unmatch", "export.csv")
	repo.CommitAll("lore notes")
	repo.Git("push", "-q", "origin", "HEAD:main")
	r := m.Lore(dir, "task", "list", "--json").OK(t)
	i := strings.Index(r.Stdout, `"id": "tsk_`)
	if i < 0 {
		t.Fatalf("no open task listed:\n%s", r.Stdout)
	}
	return r.Stdout[i+len(`"id": "`) : i+len(`"id": "`)+36]
}

// jAmLiveTasks counts the task rows on the repo's live ref.
func jAmLiveTasks(t *testing.T, git func(...string) string) int {
	t.Helper()
	n := 0
	for _, p := range strings.Split(git("ls-tree", "-r", "--name-only", "refs/lore/live"), "\n") {
		if strings.Contains(p, "/tasks/") {
			n++
		}
	}
	return n
}

func TestJourneyAdoptMoveOneCommand(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	place := tlOrg + "/" + jAmFolder
	mainRef := "refs/heads/main"
	repo := jPlainRepo(t, lee, w.Remote(jAmSlug), filepath.Join(parent, "ledger-app"))
	var openTask, invite string
	var liveBefore int

	w.Step("day1/a-repo-with-212-rows-140-open", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jAmFolder).OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", jAmFolder2).OK(t)
		openTask = jAmSeed(t, lee, repo, repo.Dir)
		if r := repo.TryGit("hook", "run", "pre-commit"); strings.Contains(r.Out(), "cannot find a hook") {
			t.Fatalf("the repo has no pre-commit hook: the story would prove nothing\n%s", r.Out())
		}
		if liveBefore = jAmLiveTasks(t, repo.Git); liveBefore < 100 {
			t.Fatalf("only %d tasks on the live ref: the story needs open (live) rows", liveBefore)
		}
		_, invite = tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
	})

	commit := "git -C " + repo.Dir + " add -A .lore/data && git -C " + repo.Dir + " commit -m \"lore: move the notes to " + place + "\""

	w.Step("day2/the-dry-run-lists-every-step-and-writes-nothing", func() {
		w.AdvanceDays(1)
		head, live := repo.Head(), repo.LocalRef("refs/lore/live")
		lee.LoreIn(repo, "store", "adopt", "--target", "team/"+jAmFolder, "--move", "--dry-run").OK(t).
			Says(t, "would adopt ", "would take ", "off the repo's live ref", "would remove ", "link: ", "then the repo commit: "+commit)
		if repo.Head() != head || repo.LocalRef("refs/lore/live") != live || repo.Status() != "" {
			t.Fatalf("the dry run changed the repo:\n%s", repo.Status())
		}
		if n := len(tt.store.FilesMatching(mainRef, jAmFolder+"/tasks/")); n != 0 {
			t.Fatalf("the dry run wrote %d tasks into the folder", n)
		}
	})

	w.Step("day2/one-command-then-the-commit-it-prints", func() {
		r := lee.LoreIn(repo, "store", "adopt", "--target", "team/"+jAmFolder, "--move", "--yes").OK(t)
		r.Says(t, "✓ adopted ", "took ", "off the repo's live ref", "review and commit it ("+commit+")", "link: ")
		if n := jAmLiveTasks(t, repo.Git); n != 0 {
			t.Fatalf("%d adopted tasks are still on the repo's live ref", n)
		}
		// the command as printed, through the real hooks
		repo.Git("add", "-A", ".lore/data")
		c := repo.TryGit("commit", "-m", "lore: move the notes to "+place)
		if c.Code != 0 || strings.Contains(c.Out(), "by removing the file") || strings.Contains(c.Out(), "E_INTERNAL") {
			t.Fatalf("the commit adopt printed failed: exit %d\n%s", c.Code, c.Out())
		}
		lee.LoreIn(repo, "show", "--id", openTask).OK(t).Never(t, "two live rows", "E_AMBIGUOUS_ID")
		if n := strings.Count(lee.LoreIn(repo, "task", "list", "--json").OK(t).Stdout, `"title": "Ledger row `); n != jAmOpen {
			t.Errorf("the repo lists %d open tasks after the move, want %d", n, jAmOpen)
		}
		repo.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "sync").OK(t)
		tasks := len(tt.store.FilesMatching(mainRef, jAmFolder+"/tasks/"))
		rules := len(tt.store.FilesMatching(mainRef, jAmFolder+"/rules/"))
		mems := len(tt.store.FilesMatching(mainRef, jAmFolder+"/memories/"))
		if tasks != jAmTasks || rules != 1 || mems != 1 {
			t.Errorf("the folder holds %d tasks, %d rules, %d memories; want %d, 1, 1", tasks, rules, mems, jAmTasks)
		}
		if out := repo.Git("ls-files", ".lore/data/tasks", ".lore/data/rules", ".lore/data/memories"); out != "" {
			t.Errorf("the repo still tracks adopted row files:\n%s", out)
		}
	})

	ann := tt.person(tlAnn)
	w.Step("day3/a-teammate-pulls-and-reads-each-row-once", func() {
		w.AdvanceDays(1)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, invite, tlAnn, []string{tlFolders})
		peer := ann.Clone(w.Remote(jAmSlug), "ledger-app")
		ann.LoreIn(peer, "sync").OK(t)
		ann.LoreIn(peer, "show", "--id", openTask).OK(t).Never(t, "two live rows", "E_AMBIGUOUS_ID")
		if n := strings.Count(ann.LoreIn(peer, "task", "list", "--json").OK(t).Stdout, `"title": "Ledger row `); n != jAmOpen {
			t.Errorf("Ann's clone lists %d open tasks, want %d", n, jAmOpen)
		}
		if n := jAmLiveTasks(t, peer.Git); n != 0 {
			t.Errorf("Ann's clone still has %d moved tasks on the live ref", n)
		}
	})

	w.Step("day3/move-and-commit-in-one-go", func() {
		two := jPlainRepo(t, lee, w.Remote(jAmSlug2), filepath.Join(parent, "ledger-two"))
		jAmSeed(t, lee, two, two.Dir)
		place2 := tlOrg + "/" + jAmFolder2
		lee.LoreIn(two, "store", "adopt", "--target", "team/"+jAmFolder2, "--move", "--commit", "--yes").OK(t).
			Says(t, "✓ adopted ", "and committed it (").Never(t, "review and commit it")
		if got := two.Git("log", "-1", "--format=%s"); got != "lore: move the notes to "+place2 {
			t.Fatalf("the repo's last commit is %q", got)
		}
		if st := two.Status(); strings.Contains(st, ".lore/data") {
			t.Fatalf("--commit left lore files uncommitted:\n%s", st)
		}
		two.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "sync").OK(t)
		if n := len(tt.store.FilesMatching(mainRef, jAmFolder2+"/tasks/")); n != jAmTasks {
			t.Errorf("the second folder holds %d tasks, want %d", n, jAmTasks)
		}
	})
}
