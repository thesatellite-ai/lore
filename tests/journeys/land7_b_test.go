//go:build journeys && !windows

package journeys

// land7_b_test.go — a person with many repos who runs `lore doctor --all`
// and quick reads all day: the doctor run stays light and in budget, and a
// trivial command starts without git or network.
//
//	day 1   install and the setup, eight repos cloned and set up
//	day 1   doctor --all in HOME: within its budget, git asked once per
//	        thing it needs (it once re-ran the refused repo open for each
//	        of its ~25 checks: 99 git runs in HOME for one answer)
//	day 1   doctor --all --fresh and again from the cache: within budget
//	day 2   lore version runs no git at all; a read in a repo asks git for
//	        a folder's top once per read, not once per question about it

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The persona (placeholders only); the setup reuses solo's GitHub account
// and personal store placeholders (solo_test.go).
const (
	land7bName  = "Lin Light"
	land7bEmail = "lin.light@example.test"
	// land7bRepos is how many repos Lin keeps on this laptop.
	land7bRepos = 8
	// land7bDoctorBudget is how long `lore doctor --all` may take on a
	// machine shared with the rest of the gate (it takes well under a
	// second alone): a run past it is a hang or a probe flood, not load.
	land7bDoctorBudget = 30 * time.Second
	// land7bStartBudget is how long a trivial command may take to start.
	land7bStartBudget = 10 * time.Second
	// land7bDoctorGitMax bounds the git runs of one cached doctor --all in
	// HOME: per repo the identity and repo reads it needs, plus a fixed
	// part for HOME and the machine. Before the read run remembered its
	// refused opens, every check opened the repo store again (two git
	// runs each, plus the setup resolution's).
	land7bDoctorGitMax = 3*land7bRepos + 16
	// land7bTopMax bounds `git rev-parse --show-toplevel` runs of one
	// `memory list` in a repo (the repo, its store engine and the personal
	// store's clone; it was 9 while every setup resolution asked again).
	land7bTopMax = 5
)

// land7bTimed runs lore in dir and fails the step when it takes longer
// than budget; it returns the result and the git commands lore ran.
func land7bTimed(t testing.TB, w *World, m *Machine, dir string, budget time.Duration, args ...string) (teamharness.Result, []string) {
	t.Helper()
	before := len(w.GitCalls(m.Name))
	start := time.Now()
	r := m.Lore(dir, args...)
	if took := time.Since(start); took > budget {
		t.Errorf("lore %s took %s, budget %s", strings.Join(args, " "), took.Round(time.Millisecond), budget)
	}
	return r, w.GitCalls(m.Name)[before:]
}

func TestJourneyLand7B(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lin := w.Persona(land7bName, land7bEmail)
	lap := lin.Laptop()
	w.Remote(soloStore) // the empty repo for the personal store
	var repos []*teamharness.Clone

	w.Step("day1/install-and-setup", func() {
		lap.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloSetupStart(t, lap, land7bName, land7bEmail)
		soloSetupRest(t, lap, "create")
		lap.Lore("", "skill", "install", "--tool", "claude_code").OK(t)
	})

	w.Step("day1/many-repos", func() {
		for i := range land7bRepos {
			c := lap.Clone(w.Remote(fmt.Sprintf("github.com/%s/svc-%d", soloLogin, i)), fmt.Sprintf("svc-%d", i))
			lap.LoreIn(c, "init").OK(t)
			lap.LoreIn(c, "memory", "add", "--body", fmt.Sprintf("svc-%d: owns its own queue", i)).OK(t)
			repos = append(repos, c)
		}
	})

	w.Step("day1/doctor-all-in-budget", func() {
		r, git := land7bTimed(t, w, lap, "", land7bDoctorBudget, "doctor", "--all", "--fresh", "--json")
		if r.Code > 2 {
			t.Errorf("doctor --all --fresh: %s", r)
		}
		// From the cache: same report, and git only for what it must read now.
		r, git = land7bTimed(t, w, lap, "", land7bDoctorBudget, "doctor", "--all", "--json")
		if r.Code > 2 {
			t.Errorf("doctor --all: %s", r)
		}
		if len(git) > land7bDoctorGitMax {
			t.Errorf("doctor --all in HOME ran git %d times for %d repos (at most %d):\n%s", len(git), land7bRepos, land7bDoctorGitMax, strings.Join(git, "\n"))
		}
		// Outside any repo the repo store is refused once per kind of open,
		// never once per check.
		if n := land7bCount(git, "rev-parse --is-bare-repository --is-inside-git-dir"); n > 2 {
			t.Errorf("doctor --all in HOME asked %d times why HOME is no work tree (at most 2: the repo engine and the store engine)", n)
		}
		// The human report, as a person reads it, also stays in budget.
		land7bTimed(t, w, lap, "", land7bDoctorBudget, "doctor", "--all")
	})

	w.AdvanceDays(1)
	w.Step("day2/startup-is-light", func() {
		r, git := land7bTimed(t, w, lap, repos[0].Dir, land7bStartBudget, "version")
		r.OK(t)
		if len(git) != 0 {
			t.Errorf("lore version ran git:\n%s", strings.Join(git, "\n"))
		}
		r, git = land7bTimed(t, w, lap, repos[0].Dir, land7bStartBudget, "memory", "list")
		r.OK(t).Says(t, "svc-0: owns its own queue")
		if n := land7bCount(git, "rev-parse --show-toplevel"); n > land7bTopMax {
			t.Errorf("memory list asked git for the repo's top %d times (at most %d):\n%s", n, land7bTopMax, strings.Join(git, "\n"))
		}
	})
}

// land7bCount is how many of the git command lines contain s.
func land7bCount(lines []string, s string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, s) {
			n++
		}
	}
	return n
}
