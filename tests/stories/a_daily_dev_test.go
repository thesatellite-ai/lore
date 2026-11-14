//go:build stories && !windows

package stories

// a_daily_dev_test.go — story A, daily development (tests/stories/A-daily-dev.md):
// a small team (cto, lead, two devs) on two service repos, one of them a
// lore 0.1.17 project the cto migrates on day 1, the other new. Tasks end to
// end, notes, the commit-msg hook, render and its pointer file, git life
// (feature branch merged, one note edited on two branches), the trash, and
// several coding-agent sessions writing at once.
//
//	TestStoryAWeek1        old-format reads, migrate run, the team starts and joins
//	TestStoryAWork         tasks, notes, commits, render, branches, trash
//	TestStoryAAgents       4 agent sessions x 5 writes at once on one repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Story A's names (placeholders).
const (
	aOrg       = "acmedev"
	aStoreSlug = "github.com/acme-dev/lore-team"
	aSvcSlug   = "github.com/acme-dev/sync-svc"
	aAuthSlug  = "github.com/acme-dev/auth-svc"
	aBrickSlug = "github.com/acme-dev/brick"
	aFolders   = "~/code/**"
	// aOldLine is the one line a write in a 0.1.17-format project answers.
	// aExitFormatOld is the exit code of E_FORMAT_OLD.
	aExitFormatOld = 24
	aOldLine       = "is on the lore 0.1.17 format; run lore migrate run to upgrade it"
)

var (
	aCTO  = journeys.Person{Name: "Cleo Tran", Email: "cleo@acme-dev.example", Login: "cleo-gh"}
	aLead = journeys.Person{Name: "Liam Reed", Email: "liam@acme-dev.example", Login: "liam-gh"}
	aDev1 = journeys.Person{Name: "Dana Ito", Email: "dana@acme-dev.example", Login: "dana-gh"}
	aDev2 = journeys.Person{Name: "Omar Vale", Email: "omar@acme-dev.example", Login: "omar-gh"}
)

// aIDs finds the row ids lore printed.
var aIDs = regexp.MustCompile(`\b(?:tsk|mem|rul|dec|msn)_[0-9a-f]{32}\b`)

// aTeam is story A's world: the people, their machines and their clones of
// the two service repos and the brick.
type aTeam struct {
	s                *Story
	store, svc, auth *teamharness.Remote
	brick            *teamharness.Remote
	m                map[string]*journeys.Machine
	c                map[string]map[string]*teamharness.Clone // email → repo slug → clone
}

// aNewTeam seeds the remotes: sync-svc a 0.1.17 project, auth-svc a code
// repo with no lore data yet, brick a repo nobody runs lore in.
func aNewTeam(t *testing.T, s *Story) *aTeam {
	t.Helper()
	at := &aTeam{s: s, store: s.Remote(aStoreSlug), m: map[string]*journeys.Machine{}, c: map[string]map[string]*teamharness.Clone{}}
	at.svc = s.Seed(s.Remote(aSvcSlug), func(dir string) { V017Repo(t, dir, "sync-svc", SmallShape) })
	at.auth = s.Seed(s.Remote(aAuthSlug), func(dir string) { PlainRepo(t, dir) })
	at.brick = s.Seed(s.Remote(aBrickSlug), func(dir string) { PlainRepo(t, dir) })
	return at
}

// person gives p a laptop with their GitHub login and clones of every repo
// under ~/code (inside the setup's folders).
func (at *aTeam) person(t *testing.T, p journeys.Person) *journeys.Machine {
	t.Helper()
	m := at.s.Person(p.Name, p.Email)
	journeys.GitHubLogin(t, at.s.World, m, p.Login)
	// The harness gives every person the standing go-ahead to push claims;
	// story A's people have not given it, as a new user has not.
	m.SetEnv(teamharness.EnvPushClaims, "0")
	at.m[p.Email], at.c[p.Email] = m, map[string]*teamharness.Clone{}
	for _, r := range []*teamharness.Remote{at.svc, at.auth, at.brick} {
		at.clone(t, p, r)
	}
	return m
}

func (at *aTeam) clone(t *testing.T, p journeys.Person, r *teamharness.Remote) *teamharness.Clone {
	t.Helper()
	m := at.m[p.Email]
	name := filepath.Base(r.Slug)
	c := m.Clone(r, name)
	m.MoveClone(t, c, filepath.Join(m.Home, "code", name))
	at.c[p.Email][r.Slug] = c
	return c
}

// in returns p's clone of the repo r.
func (at *aTeam) in(p journeys.Person, r *teamharness.Remote) *teamharness.Clone {
	return at.c[p.Email][r.Slug]
}

// lore runs lore as p in p's clone of r.
func (at *aTeam) lore(p journeys.Person, r *teamharness.Remote, args ...string) teamharness.Result {
	at.s.T.Helper()
	return at.m[p.Email].LoreIn(at.in(p, r), args...)
}

// aID returns the first row id r printed with the given prefix.
func aID(t testing.TB, r teamharness.Result, prefix string) string {
	t.Helper()
	for _, id := range aIDs.FindAllString(r.Out(), -1) {
		if strings.HasPrefix(id, prefix+"_") {
			return id
		}
	}
	t.Fatalf("no %s_ id in: %s", prefix, r)
	return ""
}

// aCount is the number of rows a list --json answer holds.
func aCount(t testing.TB, r teamharness.Result) int {
	t.Helper()
	var out struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("not a list --json answer (%v): %s", err, r)
	}
	return len(out.Data)
}

// aPush commits everything in c, merges what teammates pushed meanwhile and
// pushes with plain git (the person's own push, which may move the remote).
func aPush(c *teamharness.Clone, msg string) {
	c.CommitAll(msg)
	branch := c.Git("rev-parse", "--abbrev-ref", "HEAD")
	if c.TryGit("ls-remote", "--exit-code", "origin", "refs/heads/"+branch).Code == 0 {
		c.Git("pull", "-q", "--no-rebase", "origin", branch)
	}
	aGitPush(c, "HEAD")
}

// aGitPush is the person's own git push of ref. When rows that finished in
// live land with the push, lore's pre-push hook adds their commit and stops
// the push once with E_PUSH_AGAIN (documented, D-29 / A-13); the person runs
// the same push again and it goes through.
func aGitPush(c *teamharness.Clone, ref string) {
	r := c.TryGit("push", "-q", "origin", ref)
	if r.Code == 0 {
		return
	}
	if !strings.Contains(r.Stderr, "E_PUSH_AGAIN") || !strings.Contains(r.Stderr, "run git push again") {
		c.Git("push", "-q", "origin", ref) // fails the test with git's own words
		return
	}
	c.Git("push", "-q", "origin", ref)
}

// aStart is week 1 up to a working team: the cto migrates sync-svc, starts
// the team in auth-svc and inits it, and each of others joins. Checks of the
// standing rules along the way are part of TestStoryAWeek1; the other parts
// call it to stand the team up.
func aStart(t *testing.T, at *aTeam, others ...journeys.Person) {
	t.Helper()
	s := at.s
	cto := at.m[aCTO.Email]
	if cto == nil {
		cto = at.person(t, aCTO)
	}
	s.Step("week1/cto-migrates-sync-svc", func() {
		svc := at.in(aCTO, at.svc)
		before := s.RefsOf(at.svc)
		r := at.lore(aCTO, at.svc, "migrate", "run").OK(t).Says(t, "lore 0.1.18 format (format 2)", "git add .lore/data")
		s.NamedFlags(t, r)
		// D-8 (A-7): migrate run pushes nothing to the code remote, refs/lore/signals included.
		s.NoPushSince(t, at.svc, before, "lore migrate run")
		if len(NewFormatFiles(t, svc.Dir)) == 0 {
			t.Error("migrate run left every row file on format 1")
		}
		aPush(svc, "lore: migrate rows to format 2")
	})
	s.Step("week1/cto-starts-the-team", func() {
		auth := at.in(aCTO, at.auth)
		aStartTeam(t, cto, auth.Dir, at.store.SSHURL())
		at.lore(aCTO, at.auth, "init").OK(t)
		aPush(auth, "lore: init")
	})
	for _, p := range others {
		var line string
		s.Step("week1/invite-"+p.Login, func() {
			_, line = journeys.Invite(t, cto, at.in(aCTO, at.auth).Dir, p.Email)
		})
		m := at.person(t, p)
		s.Step("week1/join-"+p.Login, func() {
			journeys.Join(t, m, at.in(p, at.auth).Dir, "acme", line, p, []string{aFolders})
			at.lore(p, at.auth, "whoami").OK(t).Says(t, aOrg, p.Email)
		})
	}
}

// aApply answers one step of the cto's guided setup (answers is JSON),
// failing with lore setup check's reasons when lore refuses them.
func aApply(t testing.TB, m *journeys.Machine, dir, step, answers string) {
	t.Helper()
	if r := m.Lore(dir, "setup", "apply", "acme", "--step", step, "--answers", answers, "--json"); r.Code != 0 {
		c := m.Lore(dir, "setup", "check", "acme", "--step", step, "--answers", answers, "--json")
		t.Fatalf("setup apply --step %s: %s\ncheck says: %s", step, r, c.Stdout)
	}
}

// aStartTeam is the cto's guided setup as the admin of a new org: the same
// answers as journeys.StartTeam plus the org's own repos (github.com/acme-dev/*),
// so the service repos are the team's own repos (notes in the repo), not
// client repos.
func aStartTeam(t testing.TB, m *journeys.Machine, dir, storeURL string) {
	t.Helper()
	q := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	aApply(t, m, dir, "kind", `{"kind":"admin","name":"acme"}`)
	aApply(t, m, dir, "identity", `{"name":`+q(aCTO.Name)+`,"email":`+q(aCTO.Email)+`}`)
	aApply(t, m, dir, "github", `{"github_account":`+q(aCTO.Login)+`,"host_alias":"github.com"}`)
	aApply(t, m, dir, "personal_store", `{"personal_store":"local"}`)
	aApply(t, m, dir, "team", `{"org":`+q(aOrg)+`,"store_url":`+q(storeURL)+`,"own":["github.com/acme-dev/*"]}`)
	aApply(t, m, dir, "tools", `{"tools":["claude_code"]}`)
	aApply(t, m, dir, "secrets", `{"backend":"none"}`)
	aApply(t, m, dir, "folders", `{"folders":[`+q(aFolders)+`],"default":false}`)
	aApply(t, m, dir, "this_folder", `{}`)
	aApply(t, m, dir, "summary", `{}`)
	m.Lore(dir, "skill", "install", "--tool", "claude_code").OK(t)
	m.Lore(dir, "sync").OK(t)
}

// TestStoryAWeek1: the cto's first day in a 0.1.17 project, the migration,
// the team, and two people joining; the brick nobody ran lore in stays
// byte-identical and no read or write pushes anything.
func TestStoryAWeek1(t *testing.T) {
	s := New(t, "A")
	at := aNewTeam(t, s)
	at.person(t, aCTO)
	svc, brick := at.in(aCTO, at.svc), at.in(aCTO, at.brick)
	brickTree := TreeOf(t, brick.Dir)
	svcRefs := s.RefsOf(at.svc)

	s.Step("day1/reads-in-the-0.1.17-project", func() {
		r := at.lore(aCTO, at.svc, "task", "list", "--all", "--json").OK(t)
		if n := aCount(t, r); n != SmallShape["tasks"] {
			t.Errorf("task list --all shows %d of the 0.1.17 project's %d tasks", n, SmallShape["tasks"])
		}
		s.NamedFlags(t, r, at.lore(aCTO, at.svc, "migrate", "status").OK(t).Says(t, "format 1"))
	})
	s.Step("day1/a-write-before-the-migration-is-refused-in-one-line", func() {
		r := at.lore(aCTO, at.svc, "memory", "add", "--body", "retries back off").Fails(t, aExitFormatOld, aOldLine)
		s.NamedFlags(t, r)
		if bad := NewFormatFiles(t, svc.Dir); len(bad) != 0 {
			t.Errorf("a refused write raised the project's format: %v", bad[:min(3, len(bad))])
		}
		s.NoPushSince(t, at.svc, svcRefs, "reads and a refused write")
	})
	// The rest of week 1 is aStart, from the migration on.
	aStart(t, at, aLead, aDev1)
	s.Step("week1/everyone-sees-the-migrated-project", func() {
		for _, p := range []journeys.Person{aLead, aDev1} {
			r := at.lore(p, at.svc, "task", "list", "--all", "--json").OK(t)
			if n := aCount(t, r); n != SmallShape["tasks"] {
				t.Errorf("%s sees %d of the %d migrated tasks", p.Name, n, SmallShape["tasks"])
			}
			if bad := NewFormatFiles(t, at.in(p, at.svc).Dir); len(bad) == 0 {
				t.Errorf("%s's clone of sync-svc is not on the new format after the migration commit", p.Name)
			}
		}
	})
	s.Step("week1/the-brick-is-untouched", func() {
		// D-42 (A-20): a read in a repo with no lore data says what to do instead of a bare refusal.
		r := at.lore(aCTO, at.brick, "task", "list")
		if r.Code != 0 && !strings.Contains(r.Out(), "lore init") {
			t.Errorf("task list in a repo with no lore data names no next step: %s", r)
		}
		s.NamedFlags(t, r)
		s.Untouched(t, brick.Dir, brickTree, "the team's week 1 and a read in the brick")
	})
}

// TestStoryAWork: a working week of the cto and a dev in sync-svc: a
// mission and a task end to end (claim only when asked to share it, start,
// a commit naming the task, done), notes of every kind, render and its
// pointer, a feature branch with a note merged into main, one note edited on
// two branches and both merged, and the trash.
func TestStoryAWork(t *testing.T) {
	s := New(t, "A")
	at := aNewTeam(t, s)
	aStart(t, at, aDev1)
	svc, devSvc := at.in(aCTO, at.svc), at.in(aDev1, at.svc)
	var msn, task string

	s.Step("week2/plan", func() {
		refs := s.RefsOf(at.svc)
		msn = aID(t, at.lore(aCTO, at.svc, "mission", "add", "--title", "faster sync", "--json").OK(t), "msn")
		task = aID(t, at.lore(aCTO, at.svc, "task", "add", "--title", "cache the session lookup", "--mission", msn, "--priority", "high", "--json").OK(t), "tsk")
		s.NoPushSince(t, at.svc, refs, "mission add and task add")
		// Sharing the live rows is the person's call: lore sync.
		at.lore(aCTO, at.svc, "sync").OK(t)
	})
	s.Step("week2/dev-picks-it-up", func() {
		at.lore(aDev1, at.svc, "sync").OK(t)
		at.lore(aDev1, at.svc, "task", "show", "--id", task).OK(t).Says(t, "cache the session lookup")
		// D-83: a claim is shared on the code remote's signals ref, so
		// without --push it is refused and nothing moves.
		refs := s.RefsOf(at.svc)
		r := at.lore(aDev1, at.svc, "task", "claim", "--id", task).Refused(t, "--push")
		s.NamedFlags(t, r)
		s.NoPushSince(t, at.svc, refs, "a claim without --push")
		// Asked for, the claim reaches the remote (and only the signals ref moves).
		at.lore(aDev1, at.svc, "task", "claim", "--id", task, "--push").OK(t).Says(t, "claimed")
		if !at.svc.Has("refs/lore/signals") {
			t.Error("task claim --push did not share the claim on refs/lore/signals")
		}
		refs = s.RefsOf(at.svc)
		at.lore(aDev1, at.svc, "task", "start", "--id", task).OK(t).Says(t, "started")
		s.NoPushSince(t, at.svc, refs, "task start")
	})
	s.Step("week2/commit-names-the-task", func() {
		devSvc.WriteFile("cache.go", "package main\n\n// session cache\n")
		devSvc.Git("add", "cache.go")
		devSvc.Git("commit", "-q", "-m", "feat: cache the session lookup ["+task+"]")
		if msg := devSvc.Git("log", "-1", "--format=%B"); !strings.Contains(msg, task) {
			t.Errorf("the commit-msg hook dropped the task id from the message: %q", msg)
		}
		r := at.lore(aDev1, at.svc, "task", "done", "--id", task).OK(t).Says(t, "done")
		s.NamedFlags(t, r)
		at.lore(aDev1, at.svc, "sync").OK(t)
		devSvc.Git("pull", "-q", "--no-rebase", "origin", "main")
		aGitPush(devSvc, "HEAD")
	})
	var open string
	s.Step("week2/second-task-and-a-plain-git-push", func() {
		open = aID(t, at.lore(aCTO, at.svc, "task", "add", "--title", "expire idle sessions", "--mission", msn, "--json").OK(t), "tsk")
		// D-29 (A-13): a plain git push after live rows goes through, at most
		// after one E_PUSH_AGAIN that says to push again.
		at.in(aCTO, at.svc).Git("pull", "-q", "--no-rebase", "origin", "main")
		aGitPush(at.in(aCTO, at.svc), "HEAD")
	})
	s.Step("week2/mission-and-task-read-the-same-for-the-cto", func() {
		at.lore(aCTO, at.svc, "sync").OK(t)
		r := at.lore(aCTO, at.svc, "task", "show", "--id", task, "--json").OK(t)
		if !strings.Contains(r.Stdout, `"done"`) {
			t.Errorf("the cto does not see the task done: %s", r)
		}
		// D-36 (A-17): finishing a mission with an open task names that task.
		r = at.lore(aCTO, at.svc, "mission", "done", "--id", msn)
		if !strings.Contains(r.Out(), open) {
			t.Errorf("mission done with an open task does not name it: %s", r)
		}
		s.NamedFlags(t, r)
	})

	var mem string
	s.Step("week2/notes-and-render", func() {
		refs := s.RefsOf(at.svc)
		aID(t, at.lore(aCTO, at.svc, "rule", "add", "--body", "every handler takes a context first").OK(t), "rul")
		mem = aID(t, at.lore(aCTO, at.svc, "memory", "add", "--body", "the session cache holds 5 minutes").OK(t), "mem")
		aID(t, at.lore(aCTO, at.svc, "decision", "add", "--title", "keep sessions in memory", "--body", "one process per region").OK(t), "dec")
		at.lore(aCTO, at.svc, "search", "--query", "session").OK(t).Says(t, mem)
		r := at.lore(aCTO, at.svc, "render").OK(t)
		s.NamedFlags(t, r)
		s.NoPushSince(t, at.svc, refs, "notes and render")
		lore, ok := svc.ReadFile(".lore/LORE.md")
		if !ok || !strings.Contains(lore, "every handler takes a context first") {
			t.Errorf("LORE.md lacks the new rule:\n%s", lore)
		}
		// D-20 (A-9): render wires the tools chosen in the setup (Claude Code here).
		ptr, ok := svc.ReadFile("CLAUDE.md")
		if !ok || !strings.Contains(ptr, ".lore/LORE.md") {
			t.Fatalf("render left no CLAUDE.md pointer for the setup's Claude Code: %s", r)
		}
		aOnePhysicalLine(t, "CLAUDE.md", ptr)
		aPush(svc, "lore: notes")
	})

}

// TestStoryABranches: week 3 in sync-svc: a teammate pulls the cto's notes
// commit, a feature branch with a note is merged into main, one note is
// edited differently on two branches and settled, and the trash.
func TestStoryABranches(t *testing.T) {
	s := New(t, "A")
	at := aNewTeam(t, s)
	aStart(t, at, aDev1)
	svc, devSvc := at.in(aCTO, at.svc), at.in(aDev1, at.svc)
	var rule, mem string
	s.Step("week3/dev-syncs-cto-writes-notes", func() {
		at.lore(aDev1, at.svc, "sync").OK(t)
		rule = aID(t, at.lore(aCTO, at.svc, "rule", "add", "--body", "every handler takes a context first").OK(t), "rul")
		mem = aID(t, at.lore(aCTO, at.svc, "memory", "add", "--body", "the session cache holds 5 minutes").OK(t), "mem")
		at.lore(aCTO, at.svc, "render").OK(t)
		aPush(svc, "lore: notes")
	})
	s.Step("week3/teammate-pulls-the-notes", func() {
		journeys.Pending(t, "st1", "a teammate's first lore sync in a migrated repo leaves lore's wiring (.gitignore, .gitattributes, and LORE.md and LORE.full.md when it renders) uncommitted; once the cto commits the same files, the teammate's git pull aborts", func(tb testing.TB) {
			if r := devSvc.TryGit("pull", "-q", "--no-rebase", "origin", "main"); r.Code != 0 {
				tb.Errorf("Dana's git pull of the cto's notes commit aborts: %s", r)
			}
		})
		// What Dana does today: set lore's uncommitted files aside, then pull.
		if devSvc.Git("status", "--porcelain") != "" {
			devSvc.Git("stash", "-q", "-u")
			devSvc.Git("pull", "-q", "--no-rebase", "origin", "main")
			devSvc.Git("stash", "drop", "-q")
		}
		at.lore(aDev1, at.svc, "memory", "show", "--id", mem).OK(t).Says(t, "5 minutes")
	})
	s.Step("week3/feature-branch-with-a-note-merged", func() {
		devSvc.Git("pull", "-q", "--no-rebase", "origin", "main")
		devSvc.Git("checkout", "-q", "-b", "feat/expiry")
		note := aID(t, at.lore(aDev1, at.svc, "memory", "add", "--body", "expired sessions are swept hourly").OK(t), "mem")
		aPush(devSvc, "feat: sweep expired sessions")
		devSvc.Git("checkout", "-q", "main")
		// D-26 (A-12): a checkout re-renders nothing tracked.
		if st := devSvc.Git("status", "--porcelain", "--untracked-files=no"); st != "" {
			t.Errorf("switching branches left tracked files changed:\n%s", st)
		}
		// The cto merges the branch the way a PR merge does.
		svc.Git("pull", "-q", "--no-rebase", "origin", "main")
		svc.Git("fetch", "-q", "origin", "feat/expiry")
		svc.Git("merge", "-q", "--no-ff", "-m", "Merge feat/expiry", "FETCH_HEAD")
		aGitPush(svc, "main")
		at.lore(aCTO, at.svc, "memory", "show", "--id", note).OK(t).Says(t, "swept hourly")
		devSvc.Git("pull", "-q", "--no-rebase", "origin", "main")
		at.lore(aDev1, at.svc, "memory", "show", "--id", note).OK(t).Says(t, "swept hourly")
	})

	s.Step("week3/one-note-edited-on-two-branches", func() {
		svc.Git("checkout", "-q", "-b", "edit-a")
		at.lore(aCTO, at.svc, "memory", "edit", "--id", mem, "--body", "the session cache holds 10 minutes", "--yes").OK(t)
		svc.CommitAll("edit a")
		svc.Git("checkout", "-q", "main")
		svc.Git("checkout", "-q", "-b", "edit-b")
		s.Advance(60e9)
		at.lore(aCTO, at.svc, "memory", "edit", "--id", mem, "--body", "the session cache holds 15 minutes", "--yes").OK(t)
		svc.CommitAll("edit b")
		svc.Git("checkout", "-q", "main")
		svc.Git("merge", "-q", "--no-ff", "-m", "Merge edit-a", "edit-a")
		// Both branches changed the same text: lore's merge driver keeps both
		// sides inside the body value (the file stays valid JSON) and git
		// stops for the person (docs/sync/conflicts-and-duplicates.md).
		r := svc.TryGit("merge", "--no-ff", "-m", "Merge edit-b", "edit-b")
		if r.Code == 0 {
			t.Fatalf("two different edits of one note merged without asking which to keep: %s", r)
		}
		r.Says(t, "needs a manual merge")
		f := filepath.Join(svc.Dir, ".lore", "data", "memories", mem+".json")
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(b) {
			t.Errorf("the conflicted row file is not valid JSON")
		}
		at.lore(aCTO, at.svc, "sync", "conflicts").OK(t).Says(t, mem)
		// The commit is refused while the markers are there.
		if c := svc.TryGit("commit", "-qam", "merge"); c.Code == 0 {
			t.Error("git commit took a row file with conflict markers")
		}
		// Settled with a normal edit, then the merge is committed.
		at.lore(aCTO, at.svc, "memory", "edit", "--id", mem, "--body", "the session cache holds 15 minutes", "--yes").OK(t)
		svc.Git("add", "-A")
		svc.Git("commit", "-q", "-m", "Merge edit-b: 15 minutes")
		b, _ = os.ReadFile(f)
		if strings.Contains(string(b), "<<<<<<<") {
			t.Errorf("the settled row file still has conflict markers")
		}
		at.lore(aCTO, at.svc, "memory", "show", "--id", mem).OK(t).Says(t, "15 minutes").Never(t, "<<<<<<<")
	})

	s.Step("week3/trash-restore-purge", func() {
		refs := s.RefsOf(at.svc)
		at.lore(aCTO, at.svc, "memory", "delete", "--id", mem, "--yes").OK(t)
		at.lore(aCTO, at.svc, "trash", "list").OK(t).Says(t, mem)
		// D-37 (A-18): restore and purge take --id like show.
		at.lore(aCTO, at.svc, "trash", "restore", "--id", mem, "--yes").OK(t)
		at.lore(aCTO, at.svc, "memory", "show", "--id", mem).OK(t).Says(t, "15 minutes")
		at.lore(aCTO, at.svc, "rule", "delete", "--id", rule, "--yes").OK(t)
		at.lore(aCTO, at.svc, "trash", "purge", "--id", rule, "--confirm").OK(t)
		at.lore(aCTO, at.svc, "rule", "show", "--id", rule).Fails(t, teamharness.ExitRefused, "was purged")
		s.NoPushSince(t, at.svc, refs, "delete, restore and purge")
	})
}

// aOnePhysicalLine fails when a paragraph of a markdown file lore wrote is
// hard-wrapped: inside lore's pointer block every line is a whole paragraph
// (it ends a sentence or is a marker).
func aOnePhysicalLine(t testing.TB, name, body string) {
	t.Helper()
	in := false
	for _, l := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(l, "<!-- lore:pointer:start"):
			in = true
		case strings.HasPrefix(l, "<!-- lore:pointer:end"):
			in = false
		case in && l != "" && !aWhole(strings.TrimSpace(l)):
			t.Errorf("%s: a hard-wrapped line in lore's pointer: %q", name, l)
		}
	}
}

// aWhole reports whether a line of lore's pointer block is a whole
// paragraph: a sentence, an @import line or an HTML comment.
func aWhole(l string) bool {
	return strings.HasSuffix(l, ".") || strings.HasSuffix(l, ":") || strings.HasPrefix(l, "@") || (strings.HasPrefix(l, "<!--") && strings.HasSuffix(l, "-->"))
}

// aWriters and aWrites are the agent sessions that write at once and how
// many notes each writes.
const (
	aWriters = 4
	aWrites  = 5
)

// TestStoryAAgents: four coding-agent sessions of one developer write notes
// in the same repo at the same moment; no write is lost, every refusal says
// what to do next, no row file is broken, nothing is pushed.
func TestStoryAAgents(t *testing.T) {
	s := New(t, "A")
	repo := s.Seed(s.Remote(aAuthSlug), func(dir string) { PlainRepo(t, dir) })
	dev := s.Person(aDev2.Name, aDev2.Email)
	c := dev.Clone(repo, "auth-svc")
	s.Step("day1/init", func() {
		dev.LoreIn(c, "init").OK(t)
		aPush(c, "lore: init")
	})
	s.Step("day1/agents-write-at-once", func() {
		refs := s.RefsOf(repo)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var failed []teamharness.Result
		for i := range aWriters {
			agent := dev.Actor.Agent(fmt.Sprintf("claude-%d", i))
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range aWrites {
					args := []string{"memory", "add", "--body", fmt.Sprintf("agent %d note %d", i, j)}
					if j%2 == 1 {
						args = []string{"task", "add", "--title", fmt.Sprintf("agent %d task %d", i, j), "--commitment", "accepted"}
					}
					if r := agent.Lore(c.Dir, args...); r.Code != 0 {
						mu.Lock()
						failed = append(failed, r)
						mu.Unlock()
					}
				}
			}()
		}
		wg.Wait()
		for _, r := range failed {
			t.Errorf("a concurrent agent write failed: %s", r)
		}
		mems := aCount(t, dev.LoreIn(c, "memory", "list", "--json").OK(t))
		tasks := aCount(t, dev.LoreIn(c, "task", "list", "--all", "--json").OK(t))
		wantT := aWriters * (aWrites / 2)
		if mems != aWriters*aWrites-wantT || tasks != wantT {
			t.Errorf("after %d agents x %d writes: %d memories and %d tasks, want %d and %d (a lost write)", aWriters, aWrites, mems, tasks, aWriters*aWrites-wantT, wantT)
		}
		s.NoPushSince(t, repo, refs, "concurrent agent writes")
		dev.LoreIn(c, "doctor").Never(t, "not a usable row file")
	})
}
