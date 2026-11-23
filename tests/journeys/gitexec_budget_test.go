//go:build journeys && !windows

package journeys

// gitexec_budget_test.go — every everyday command starts a bounded number
// of git processes (devco:D-418). One `task list` once started 121 git
// processes; at ~20 ms per process on macOS that alone was most of a
// second. The gitfast Session answers repository reads in-process now;
// these budgets keep the count from creeping back. A command over its
// budget fails with the git argv it ran, so the new call is easy to find.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitBudget is the most git processes one command may start (counted by
// the World's PATH shim; hooks git runs inside are not counted). The
// owner's targets are a read at most 5 and a write at most 10; each budget
// is the measured count plus one, so any new git call on these paths fails.
// Every write also refreshes the renders it staled (devco:D-921); that
// refresh reuses what the write already knows and starts no git process
// of its own (devco:D-945, bug hunt H-203: memory add once started 4, a
// write to a team store one more): the rule, hotfix and decision adds
// re-render .lore/LORE.md and still start only what a memory add does.
var gitBudget = []struct {
	name string
	args []string
	max  int
}{
	{"task list", []string{"task", "list"}, 2},
	{"task show", nil, 2}, // args filled in with the task id
	{"search", []string{"search", "--query", "budget"}, 2},
	{"render", []string{"render"}, 4},
	{"task add", []string{"task", "add", "Second budget task", "--commitment", "accepted"}, 8},
	{"memory add", []string{"memory", "add", "--body", "budget memory"}, 2},
	{"rule add", []string{"rule", "add", "--body", "Budget rule: never log tokens", "--severity", "must"}, 3},
	{"hotfix add", []string{"hotfix", "add", "--title", "Budget hotfix", "--body", "Rerun the job once", "--severity", "high"}, 3},
	{"decision add", []string{"decision", "add", "--title", "Budget decision", "--body", "Local first; no server."}, 3},
	{"doctor", []string{"doctor"}, 8},
}

func TestJourneyGitExecBudget(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dev := w.Persona("Bea Budget", "bea.budget@example.test")
	m := dev.Laptop()
	app := m.Clone(w.Remote("github.com/bea/abacus"), "abacus")
	var id string
	w.Step("day1/init", func() {
		app.Lore("init", "--non-interactive", "--name=abacus").OK(t)
		app.Lore("task", "add", "Budget task one", "--commitment", "accepted").OK(t)
		out := app.Lore("task", "list", "--json").OK(t).Stdout
		i := strings.Index(out, "tsk_")
		if i < 0 {
			t.Fatalf("no task id in %q", out)
		}
		id = out[i : i+4+32]
	})
	w.Step("day1/git-exec-budgets", func() {
		for _, c := range gitBudget {
			args := c.args
			if args == nil {
				args = []string{"task", "show", "--id", id}
			}
			before := len(w.GitCalls(m.Name))
			if r := app.Lore(args...); r.Code != 0 && c.name != "doctor" { // doctor exits 1 on a warning
				t.Fatalf("%s: exit %d: %s", c.name, r.Code, r.Out())
			}
			calls := w.GitCalls(m.Name)[before:]
			t.Logf("%s: %d git processes (budget %d)", c.name, len(calls), c.max)
			if len(calls) > c.max {
				t.Errorf("%s started %d git processes, budget %d (devco:D-418):\n  %s", c.name, len(calls), c.max, strings.Join(calls, "\n  "))
			}
		}
	})
}

// gitBudgetTeam is gitBudget for a repo whose org has a team store and a
// personal store (each command also reads both store clones).
var gitBudgetTeam = map[string]int{
	"task list": 3, "task show": 4, "search": 3, "render": 3, "task add": 5, "memory add": 7, "rule add": 7, "hotfix add": 7, "decision add": 7, "doctor": 30,
}

func TestJourneyGitExecBudgetTeam(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	repo := tt.repo[tlLee.Email]
	var id string
	w.Step("day1/team-store-attached", func() {
		tlStartTeam(t, lee, repo.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.LoreIn(repo, "store", "folder", "add", "team", "app").OK(t)
		lee.LoreIn(repo, "org", "link", tlOrg, "--project", "app", "--yes").OK(t)
		lee.LoreIn(repo, "task", "add", "Budget task one", "--commitment", "accepted").OK(t)
		out := lee.LoreIn(repo, "task", "list", "--json").OK(t).Stdout
		i := strings.Index(out, "tsk_")
		if i < 0 {
			t.Fatalf("no task id in %q", out)
		}
		id = out[i : i+4+32]
	})
	w.Step("day1/git-exec-budgets-with-stores", func() {
		for _, c := range gitBudget {
			args := c.args
			if args == nil {
				args = []string{"task", "show", "--id", id}
			}
			max := gitBudgetTeam[c.name]
			before := len(w.GitCalls(lee.Name))
			if r := lee.LoreIn(repo, args...); r.Code != 0 && c.name != "doctor" {
				t.Fatalf("%s: exit %d: %s", c.name, r.Code, r.Out())
			}
			calls := w.GitCalls(lee.Name)[before:]
			t.Logf("%s: %d git processes (budget %d)", c.name, len(calls), max)
			if len(calls) > max {
				t.Errorf("%s started %d git processes with a team store, budget %d (devco:D-418):\n  %s", c.name, len(calls), max, strings.Join(calls, "\n  "))
			}
		}
	})
}

// hookBudget is the most git processes lore (and its hook blocks' shell
// text) may start in the hooks of one git command (devco:D-420). git's own
// subprocesses (the commit itself, its auto maintenance, what a push's
// transport starts) are not counted: lore cannot change them. The owner's
// target is at most 5 for a plain commit; each budget is the measured
// count. A push here is 4 (the leak check's rev-list --objects and
// diff-tree of the pushed commits, the data folder status, the store
// clone's status; its other rev-lists of the pushed range are answered
// in-process, devco:D-780); with live rows waiting it also pushes
// refs/lore/live and lists the commits it sends.
var hookBudget = map[string]int{"commit": 5, "commit -a": 5, "push": 5}

// hookExecPath is a git exec path whose git logs every call with its
// parent process, then runs the real git. git puts its exec path first on
// the PATH of the hooks it runs, so the World's PATH shim never sees the
// hooks' git calls; this one sees lore's and git's own, told apart by the
// parent (git's own have a git parent).
func hookExecPath(t *testing.T) (dir, log string) {
	t.Helper()
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skipf("git --exec-path: %v", err)
	}
	gitCore := strings.TrimSpace(string(out))
	dir, log = t.TempDir(), filepath.Join(t.TempDir(), "hook-git.log")
	ents, err := os.ReadDir(gitCore)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name() == "git" {
			continue
		}
		if err := os.Symlink(filepath.Join(gitCore, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	shim := "#!/bin/sh\np=$(ps -o comm= -p $PPID); p=${p##*/}\nprintf '%s\\t%s\\n' \"$p\" \"$*\" >> '" + log + "'\nexec '" + filepath.Join(gitCore, "git") + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, log
}

// loreStarted reads the shim log: the calls whose parent is not git.
func loreStarted(t *testing.T, log string) (lore, all []string) {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line == "" {
			continue
		}
		all = append(all, line)
		if parent, _, _ := strings.Cut(line, "\t"); !strings.HasPrefix(parent, "git") {
			lore = append(lore, line)
		}
	}
	return lore, all
}

// TestJourneyHookGitExecBudget: the hooks of one plain commit once started
// 17 git processes, 7-11 of them lore's: every `lore sync hook` counted as
// `lore sync` and fetched each org store and asked its HEAD (ls-remote)
// whatever live.fetch_ttl said, and `git commit -a`'s temporary index made
// every index read run git. Now lore starts at most 5 (devco:D-420).
func TestJourneyHookGitExecBudget(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	repo := tt.repo[tlLee.Email]
	w.Step("day1/team-store-attached", func() {
		tlStartTeam(t, lee, repo.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.LoreIn(repo, "init", "--non-interactive", "--name=app").OK(t) // every hook block: pre-commit, commit-msg, post-commit, pre-push
	})
	w.Step("day1/hook-git-exec-budgets", func() {
		xp, log := hookExecPath(t)
		g := repo.With("GIT_EXEC_PATH=" + xp)
		// the first commits carry what init wrote and fill lore's caches
		// (under the same exec path: it is on the hooks' PATH, which keys
		// the cached config); the budgets are for the everyday commit after
		for i, msg := range []string{"lore init", "warm up"} {
			if err := os.WriteFile(filepath.Join(repo.Dir, "warm.txt"), []byte(msg), 0o644); err != nil {
				t.Fatal(err)
			}
			g.Git("add", "-A")
			if r := g.TryGit("commit", "-q", "-m", msg); r.Code != 0 {
				t.Fatalf("commit %d: exit %d: %s", i, r.Code, r.Out())
			}
		}
		write := func(body string) {
			if err := os.WriteFile(filepath.Join(repo.Dir, "budget.txt"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run := func(name string, args ...string) {
			t.Helper()
			if err := os.WriteFile(log, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if r := g.TryGit(args...); r.Code != 0 && name != "push" { // a refused push still ran its hook
				t.Fatalf("git %s: exit %d: %s", name, r.Code, r.Out())
			}
			lore, all := loreStarted(t, log)
			t.Logf("%s: lore started %d git processes, git %d of its own (budget %d)", name, len(lore), len(all)-len(lore), hookBudget[name])
			if len(lore) > hookBudget[name] {
				t.Errorf("the hooks of git %s started %d git processes, budget %d (devco:D-420):\n  %s", name, len(lore), hookBudget[name], strings.Join(all, "\n  "))
			}
		}
		write("one\n")
		repo.Git("add", "budget.txt")
		run("commit", "commit", "-q", "-m", "budget one")
		write("two\n")
		run("commit -a", "commit", "-q", "-a", "-m", "budget two")
		run("push", "push", "-q", "origin", "HEAD:refs/heads/budget")
	})
}

// TestJourneyWarmReadOutsideRepoRunsNoGit: an agent asking its personal
// store from a folder outside every repository starts no git process once
// the caches are warm, and gets the same answer as the cold call
// (devco:D-826: each such read started ten git processes, rev-parse
// asking "where is the repository?" to hear "none", plus a config
// listing).
func TestJourneyWarmReadOutsideRepoRunsNoGit(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ida := w.Persona("Ida Field", "ida.field@acme-co.example")
	lap := ida.Laptop()
	me := w.Remote("github.com/ida-field/lore-me")
	w.Step("day1/notes-in-the-personal-store", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		lap.Lore(lap.Home, "store", "folder", "add", "me", "work").OK(t)
		lap.Lore(lap.Home, "memory", "add", "--body", "Status update orbit: live with 9 customers.", "--to", "me/work", "--yes").OK(t)
	})
	w.Step("day1/ask-twice-from-home", func() {
		cold := lap.Lore(lap.Home, "ask", "--store", "me", "--query", "what is the status of orbit").OK(t).
			Says(t, "live with 9 customers").Out()
		before := len(w.GitCalls(lap.Name))
		warm := lap.Lore(lap.Home, "ask", "--store", "me", "--query", "what is the status of orbit").OK(t).Out()
		if calls := w.GitCalls(lap.Name)[before:]; len(calls) != 0 {
			t.Errorf("a warm read outside every repository started %d git processes (want 0):\n  %s", len(calls), strings.Join(calls, "\n  "))
		}
		if warm != cold {
			t.Errorf("the warm answer differs from the cold one:\ncold:\n%s\nwarm:\n%s", cold, warm)
		}
	})
}
