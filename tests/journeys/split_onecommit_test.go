//go:build journeys && !windows

package journeys

// split_onecommit_test.go — a lead breaks one big task into 150 sub-tasks
// with `lore task split --from` (finding run:plat:split-perf). The split
// once wrote every child as its own live commit: ~19 git runs per child,
// ~2,900 for 150 children and minutes of wall time. Now the children land
// as ONE live commit, the git runs stay bounded whatever the count, and
// the person sees the same result: every child, in order, under the
// parent, on one tasklist, and a re-run adds nothing.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	// splitChildren is how many sub-tasks the lead splits into.
	splitChildren = 150
	// splitGitMax bounds the git runs of the whole split: the fixed reads
	// of one command plus one live commit, not a number per child (the
	// old per-child commits took ~19 each, 2,900 for 150).
	splitGitMax = 120
)

// splitTaskID finds task ids in output.
var splitTaskID = regexp.MustCompile(`tsk_[0-9a-f]{32}`)

func TestJourneySplitOneCommit(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lead := w.Persona("Lena Lead", "lena.lead@example.test")
	m := lead.Laptop()
	app := m.Clone(w.Remote("github.com/lena/ledger"), "ledger")
	var parent string

	w.Step("day1/init-and-one-big-task", func() {
		app.Lore("init", "--non-interactive", "--name=ledger").OK(t)
		add := app.Lore("task", "add", "Rewrite the ledger import", "--commitment", "accepted").OK(t)
		parent = splitTaskID.FindString(add.Stdout)
		if parent == "" {
			t.Fatalf("task add printed no id:\n%s", add.Stdout)
		}
	})

	w.Step("day1/split-150-in-one-commit", func() {
		var b strings.Builder
		for i := 1; i <= splitChildren; i++ {
			fmt.Fprintf(&b, "Import step %03d\n", i)
		}
		app.WriteFile("steps.txt", b.String())
		liveBefore := splitLiveCommits(t, app.Git)
		gitBefore := len(w.GitCalls(m.Name))
		start := time.Now()
		r := app.Lore("task", "split", "--id", parent, "--from", "steps.txt", "--key", "ledger-import").OK(t)
		took := time.Since(start)
		git := w.GitCalls(m.Name)[gitBefore:]
		r.Says(t, "150 sub-tasks under "+parent)
		if got := splitLiveCommits(t, app.Git) - liveBefore; got != 1 {
			t.Errorf("split of %d children made %d live commits, want 1", splitChildren, got)
		}
		if len(git) > splitGitMax {
			t.Errorf("split of %d children ran git %d times, want at most %d; first runs:\n%s",
				splitChildren, len(git), splitGitMax, strings.Join(git[:min(len(git), 40)], "\n"))
		}
		t.Logf("split of %d children: %d git runs, %s", splitChildren, len(git), took.Round(time.Millisecond))
		// every child printed once, numbered in order, each with its own id
		ids := map[string]bool{}
		for i := 1; i <= splitChildren; i++ {
			title := fmt.Sprintf("Import step %03d", i)
			line := "  " + strconv.Itoa(i) + "  "
			found := false
			for _, l := range strings.Split(r.Stdout, "\n") {
				if strings.HasPrefix(l, line) && strings.HasSuffix(l, title) {
					id := splitTaskID.FindString(l)
					if id == "" || ids[id] {
						t.Errorf("line %d has no id or a repeated one: %q", i, l)
					}
					ids[id], found = true, true
				}
			}
			if !found {
				t.Errorf("split output has no line %d %q:\n%s", i, title, r.Stdout)
				break
			}
		}
	})

	w.Step("day1/children-in-order-on-one-list", func() {
		out := app.Lore("task", "list", "--parent", parent, "--json").OK(t).Stdout
		titles := regexp.MustCompile(`"title":\s*"(Import step \d{3})"`).FindAllStringSubmatch(out, -1)
		if len(titles) != splitChildren {
			t.Fatalf("task list --parent shows %d children, want %d", len(titles), splitChildren)
		}
		for i, m := range titles {
			if want := fmt.Sprintf("Import step %03d", i+1); m[1] != want {
				t.Fatalf("child %d is %q, want %q (rank order)", i+1, m[1], want)
			}
		}
		// one default list holds the parent and every child (a split that
		// made a list per child would show more lists, each with fewer tasks)
		tl := app.Lore("tasklist", "list").OK(t)
		tl.Says(t, "default [active]  ("+strconv.Itoa(splitChildren+1)+" tasks)")
		if n := strings.Count(tl.Stdout, "tlt_"); n != 1 {
			t.Errorf("tasklist list shows %d lists, want 1:\n%s", n, tl.Stdout)
		}
	})

	w.Step("day1/rerun-adds-nothing", func() {
		before := splitLiveCommits(t, app.Git)
		app.Lore("task", "split", "--id", parent, "--from", "steps.txt", "--key", "ledger-import").OK(t)
		if got := splitLiveCommits(t, app.Git) - before; got != 0 {
			t.Errorf("a re-run with the same --key made %d live commits, want 0", got)
		}
		app.Lore("tasklist", "list").OK(t).Says(t, "("+strconv.Itoa(splitChildren+1)+" tasks)")
	})
}

// splitLiveCommits counts the commits on the repo's live ref.
func splitLiveCommits(t *testing.T, git func(...string) string) int {
	t.Helper()
	n, err := strconv.Atoi(git("rev-list", "--count", "refs/lore/live"))
	if err != nil {
		t.Fatal(err)
	}
	return n
}
