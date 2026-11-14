//go:build stories && !windows

package stories

// D_new_stacks_test.go — story D (tests/stories/D-new-stacks.md): lore
// adopted from zero in repos on other stacks (a monorepo with agent files of
// its own, an app whose CLAUDE.md is generated, a tool nobody inits), then
// used in the checkouts real people make: worktrees, a detached HEAD from
// before lore, a depth-1 CI clone, a sparse cone, and two weeks of daily work
// by developers and agent sessions at once. The journeys
// TestJourneyCheckoutsOfALoreRepo, TestJourneyRenderedFiles and
// TestJourneyNoLoreRepoGlobalHooksPath check single steps in depth; this
// replays them as one story with the standing rules after each step.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	dMonoSlug = "github.com/orbit-test/mono"
	dAppSlug  = "github.com/orbit-test/shopfront"
	dToolSlug = "github.com/orbit-test/conftool"
	dLinkSlug = "github.com/orbit-test/linked"

	// dGeneratedClaude is a CLAUDE.md another tool generates: lore must not
	// stitch its pointer into it (D-58).
	dGeneratedClaude = "<!-- GENERATED FILE — do not edit directly; run make agents -->\n# Shopfront\n\nRun `composer test` before a push.\n"
	// dRootAgents and dCursorRule are agent files the monorepo had before
	// lore: kept byte for byte apart from lore's own pointer block.
	dRootAgents = "# Agents\n\nUse pnpm, never npm.\n"
	dCursorRule = "---\ndescription: house style\n---\nPrefer small functions.\n"
	// dPkgVersion is the monorepo's package.json version, which whoami must
	// never report as an agent's version (D-60).
	dPkgVersion = "7.7.7-mono"
)

func TestStoryDNewStacks(t *testing.T) {
	s := New(t, "D")
	mono := s.Seed(s.Remote(dMonoSlug), func(dir string) {
		Monorepo(t, dir)
		writeFile(t, filepath.Join(dir, "AGENTS.md"), dRootAgents)
		writeFile(t, filepath.Join(dir, ".cursor", "rules", "style.mdc"), dCursorRule)
		writeFile(t, filepath.Join(dir, "package.json"), "{\n  \"name\": \"mono\",\n  \"version\": \""+dPkgVersion+"\",\n  \"private\": true\n}\n")
	})
	app := s.Seed(s.Remote(dAppSlug), func(dir string) {
		writeFile(t, filepath.Join(dir, "composer.json"), "{\n  \"name\": \"orbit/shopfront\"\n}\n")
		writeFile(t, filepath.Join(dir, "src", "Kernel.php"), "<?php\n\nclass Kernel {}\n")
		writeFile(t, filepath.Join(dir, "CLAUDE.md"), dGeneratedClaude)
	})
	tool := s.Seed(s.Remote(dToolSlug), func(dir string) { PlainRepo(t, dir) })
	linked := s.Remote(dLinkSlug)
	all := []*teamharness.Remote{mono, app, tool, linked}

	cto := s.Person("Ines Cato", "ines.cato@example.test")
	dev1 := s.Person("Kofi Lind", "kofi.lind@example.test")
	dev2 := s.Person("Mara Voss", "mara.voss@example.test")
	// The harness gives every machine a standing go-ahead to share claims;
	// these people have none, so a claim is shared only when asked (C13).
	for _, m := range []*journeys.Machine{cto, dev1, dev2} {
		m.Actor.SetEnv(teamharness.EnvPushClaims, "0")
	}

	var cm, d1, d2, ca, ct *teamharness.Clone
	var toolTree string

	s.Step("day1/init-at-the-monorepo-root", func() {
		cm = cto.Clone(mono, "mono")
		ct = cto.Clone(tool, "conftool")
		toolTree = TreeOf(t, ct.Dir)
		before := dSnap(s, all...)
		r := cto.LoreIn(cm, "init").OK(t)
		s.NamedFlags(t, r)
		if !dExists(cm.Dir, ".lore") {
			t.Fatalf("init made no .lore at the monorepo root:\n%s", r)
		}
		for _, sub := range []string{"services/billing/.lore", "libs/ui/.lore"} {
			if dExists(cm.Dir, sub) {
				t.Errorf("root init made %s", sub)
			}
		}
		agents := dRead(t, cm.Dir, "AGENTS.md")
		if !strings.Contains(agents, dRootAgents) || strings.Count(agents, ".lore/LORE.md") == 0 {
			t.Errorf("AGENTS.md lost its own text or got no pointer:\n%s", agents)
		}
		cto.LoreIn(cm, "render").OK(t)
		if again := dRead(t, cm.Dir, "AGENTS.md"); again != agents && strings.Count(again, "Read `.lore/LORE.md`") > 1 {
			t.Errorf("render duplicated the AGENTS.md pointer:\n%s", again)
		}
		if got := dRead(t, cm.Dir, ".cursor/rules/style.mdc"); got != dCursorRule {
			t.Errorf("lore clobbered the existing cursor rule:\n%s", got)
		}
		dNoPush(t, s, before, "lore init and render")
		cm.CommitAll("chore: adopt lore")
		cm.Git("push", "-q", "origin", "HEAD:main")
	})

	s.Step("day1/package-init-is-its-own-project", func() {
		d1 = dev1.Clone(mono, "mono")
		before := dSnap(s, all...)
		pkg := d1.In(filepath.Join("services", "billing"))
		r := dev1.LoreIn(pkg, "init", "--non-interactive").OK(t).
			Says(t, "name:       mono-billing", "already holds a lore project in the repo root")
		s.NamedFlags(t, r)
		got := dRead(t, d1.Dir, "services/billing/AGENTS.md")
		if !strings.Contains(got, "Read `services/billing/.lore/LORE.md`") {
			t.Errorf("the package pointer does not name the package's own LORE.md (D-103):\n%s", got)
		}
		dNoPush(t, s, before, "package init")
		d1.CommitAll("chore(billing): lore sub-project")
		d1.Git("push", "-q", "origin", "HEAD:main")
	})

	s.Step("day2/generated-claude-md-is-left-alone", func() {
		s.AdvanceDays(1)
		ca = cto.Clone(app, "shopfront")
		before := dSnap(s, all...)
		r := cto.LoreIn(ca, "init").OK(t)
		if got := dRead(t, ca.Dir, "CLAUDE.md"); got != dGeneratedClaude {
			t.Errorf("init stitched its pointer into a generated CLAUDE.md (D-58):\n%s\ninit said:\n%s", got, r)
		}
		r = cto.LoreIn(ca, "init", "--target", "cursor").Refused(t, "already exists", "lore render --add-target cursor").Never(t, "nothing to do")
		s.NamedFlags(t, r)
		cto.LoreIn(ca, "render", "--add-target", "cursor").OK(t)
		if got := dRead(t, ca.Dir, "CLAUDE.md"); got != dGeneratedClaude {
			t.Errorf("render --add-target changed the generated CLAUDE.md:\n%s", got)
		}
		dNoPush(t, s, before, "init in the app")
		ca.CommitAll("chore: adopt lore")
		ca.Git("push", "-q", "origin", "HEAD:main")
	})

	s.Step("day2/claude-md-linked-to-agents-md", func() {
		l := cto.Clone(linked, "linked")
		l.WriteFile("AGENTS.md", "# agents\n")
		if err := os.Symlink("AGENTS.md", filepath.Join(l.Dir, "CLAUDE.md")); err != nil {
			t.Fatal(err)
		}
		cto.LoreIn(l, "init").OK(t)
		cto.LoreIn(l, "render").OK(t)
		cto.LoreIn(l, "render").OK(t).Says(t, "one pointer block serves both").Never(t, "written through the link")
		got := dRead(t, l.Dir, "AGENTS.md")
		if !strings.Contains(got, "@.lore/LORE.md\n") || strings.Count(got, "Read `.lore/LORE.md`") != 1 {
			t.Errorf("AGENTS.md (CLAUDE.md links to it) lost the @import or has the pointer twice (D-104):\n%s", got)
		}
	})

	s.Step("day3/worktree-and-pre-lore-checkout", func() {
		s.AdvanceDays(1)
		before := dSnap(s, all...)
		wt := s.Worktree(t, d1.Dir, "feat-ledger")
		w := dev1.OpenClone(mono, wt)
		dev1.LoreIn(w, "task", "add", "--title", "ledger rounding in a worktree", "--commitment", "accepted").OK(t)
		w.WriteFile("services/ledger/internal/core/round.go", "package core\n\n// Round rounds half to even.\nfunc Round() {}\n")
		r := w.TryGit("add", "-A")
		if r.Code != 0 {
			t.Fatalf("git add in the worktree: %s", r)
		}
		r = w.TryGit("commit", "-q", "-m", "feat(ledger): rounding")
		if r.Code != 0 {
			t.Fatalf("a commit in a worktree failed:\n%s", r)
		}
		if bad := filepath.Join(wt, ".git", "config"); strings.Contains(r.Out(), bad) {
			t.Errorf("the hook names %s, a path inside a file (D-61):\n%s", bad, r.Out())
		}
		if got := w.Git("ls-files", ".lore/data"); got == "" {
			t.Errorf("the worktree commit carried no lore rows")
		}
		old := filepath.Join(filepath.Dir(d1.Dir), "mono-before-lore")
		first := d1.Git("rev-list", "--max-parents=0", "HEAD")
		d1.Git("worktree", "add", "-q", "--detach", old, first)
		oc := dev1.OpenClone(mono, old)
		r = dev1.LoreIn(oc, "task", "list").Refused(t, "from before lore was added", "git switch").Never(t, "lore init sets this repo up")
		s.NamedFlags(t, r)
		if dExists(old, ".lore") {
			t.Errorf("a refused read made .lore in the pre-lore checkout")
		}
		dNoPush(t, s, before, "worktree work and a detached checkout")
	})

	s.Step("day3/ci-depth-1-clone", func() {
		cto.LoreIn(cm, "rule", "add", "--to", "repo", "--no-live", "--body", "money is integer cents").OK(t)
		cto.LoreIn(cm, "render").OK(t)
		cm.CommitAll("chore: a rule")
		cm.Git("pull", "-q", "--no-rebase", "origin", "main")
		cm.Git("push", "-q", "origin", "HEAD:main")
		before := dSnap(s, all...)
		runner := s.Persona("CI Runner", "ci.runner@example.test").Laptop()
		ci := s.ShallowClone(t, runner, mono, "mono-ci", 1)
		runner.SetEnv("CI", "true")
		runner.SetEnv("GITHUB_ACTIONS", "true")
		status := ci.Git("status", "--porcelain", "--ignored")
		runner.LoreIn(ci, "render", "--check").OK(t).Never(t, "E_REFUSED")
		runner.LoreIn(ci, "rule", "list").OK(t).Says(t, "integer cents")
		runner.LoreIn(ci, "search", "--query", "cents").OK(t).Says(t, "integer cents")
		if got := ci.Git("status", "--porcelain", "--ignored"); got != status {
			t.Errorf("CI reads changed the clone:\nbefore:\n%s\nafter:\n%s", status, got)
		}
		dNoPush(t, s, before, "CI reads")
	})

	s.Step("day4/sparse-cone-without-lore", func() {
		s.AdvanceDays(1)
		d2 = dev2.Clone(mono, "mono")
		d2.Git("sparse-checkout", "set", "--cone", "services/gateway")
		if dExists(d2.Dir, ".lore/data") {
			t.Fatalf("the cone still holds .lore/data; the step tests nothing")
		}
		r := dev2.LoreIn(d2, "task", "list").Refused(t, "sparse checkout", "git sparse-checkout add .lore").Never(t, "lore init sets this repo up")
		s.NamedFlags(t, r)
		r = dev2.LoreIn(d2.In(filepath.Join("services", "gateway")), "task", "list")
		if strings.Contains(r.Out(), "ledger rounding") {
			t.Errorf("a package folder in the cone lists the root project's tasks as its own (D-74):\n%s", r)
		}
		d2.Git("sparse-checkout", "disable")
	})

	s.Step("week1/daily-work-by-people-and-agents", func() {
		before := dSnap(s, all...)
		d2.Git("pull", "-q", "--no-rebase", "origin", "main")
		for _, w := range []struct {
			m    *journeys.Machine
			c    *teamharness.Clone
			pkg  string
			text string
		}{
			{cto, cm, "services/billing", "invoice totals in cents"},
			{dev1, d1, "services/gateway", "gateway retries with jitter"},
			{dev2, d2, "libs/ui", "button focus ring contrast"},
		} {
			s.Advance(time.Minute)
			w.m.LoreIn(w.c.In(w.pkg), "task", "add", "--title", w.text, "--commitment", "accepted").OK(t)
		}
		cto.LoreIn(cm, "decision", "add", "--title", "pnpm workspaces", "--body", "one lockfile at the root").OK(t)
		cto.LoreIn(cm, "plan", "add", "--title", "billing v2", "--body", "cents everywhere, then the new invoice layout").OK(t)
		for _, label := range []string{"agent-a", "agent-b", "agent-c"} {
			ag := cm.As(cto.Agent(label))
			ag.Lore("task", "add", "--title", "proposed by "+label, "--commitment", "proposed").OK(t)
		}
		cto.LoreIn(cm, "task", "list").OK(t).Says(t, "3 hidden (3 proposed)", "--include-proposed")
		cto.LoreIn(cm, "task", "list", "--include-proposed").OK(t).Says(t, "proposed by agent-b")
		// dev1 searches from the repo root for the task they filed in a
		// package folder: search spans packages. (The cto's clone cannot
		// see it yet: nothing was pushed, and rule 1 says nothing is.)
		dev1.LoreIn(d1, "search", "--query", "jitter").OK(t).Says(t, "gateway retries with jitter")
		who := cm.As(cto.Agent("agent-a")).Lore("whoami").OK(t)
		if strings.Contains(who.Out(), dPkgVersion) {
			t.Errorf("whoami reports the repo's package.json version as the agent's (D-60):\n%s", who)
		}
		dNoPush(t, s, before, "a day of task, decision, plan, search and whoami")
	})

	s.Step("week1/conventional-commits-and-a-claim", func() {
		cm.WriteFile("services/billing/internal/core/core.go", "package core\n\n// v2\n")
		start := time.Now()
		r := cm.TryGit("commit", "-qam", "feat(billing): totals v2")
		took := time.Since(start)
		if r.Code != 0 {
			t.Fatalf("a conventional-commit message failed the hooks:\n%s", r)
		}
		t.Logf("code-only commit with lore hooks took %s (D-94: was ~1.7 s)", took)
		if took > 5*time.Second {
			t.Errorf("a code-only commit took %s with lore's hooks (D-94)", took)
		}
		var added struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		r = cto.LoreIn(cm, "task", "add", "--title", "squash me", "--commitment", "accepted", "--json").OK(t)
		if err := json.Unmarshal([]byte(r.Stdout), &added); err != nil || added.Data.ID == "" {
			t.Fatalf("task add --json: %v\n%s", err, r)
		}
		before := dSnap(s, all...)
		cto.LoreIn(cm, "task", "claim", "--id", added.Data.ID).Refused(t, "--push", "refs/lore/signals")
		dNoPush(t, s, before, "a claim without --push")
		cto.LoreIn(cm, "task", "claim", "--id", added.Data.ID, "--push").OK(t)
		cm.Git("switch", "-q", "-c", "feat-squash")
		cm.WriteFile("services/billing/README.md", "# billing\n\nsquash work\n")
		cm.CommitAll("feat(billing): squash work")
		msg := cm.Git("log", "-1", "--format=%B")
		if !strings.Contains(msg, "Lore-Task:") {
			t.Errorf("the commit on a claimed task carries no Lore-Task trailer, a squash would lose the link (D-82):\n%s", msg)
		}
		cm.Git("switch", "-q", "main")
		cm.Git("merge", "-q", "--squash", "feat-squash")
		cm.Git("commit", "-q", "-m", "feat(billing): squash work (#12)\n\n"+trailerLines(msg))
	})

	s.Step("week2/rules-on-two-branches-merge", func() {
		s.AdvanceDays(7)
		cm.Git("pull", "-q", "--no-rebase", "origin", "main")
		cm.Git("push", "-q", "origin", "HEAD:main")
		d1.Git("pull", "-q", "--no-rebase", "origin", "main")
		cto.LoreIn(cm, "rule", "add", "--to", "repo", "--no-live", "--body", "ids are ULIDs").OK(t)
		dev1.LoreIn(d1, "rule", "add", "--to", "repo", "--no-live", "--body", "logs are JSON lines").OK(t)
		cm.CommitAll("chore: rule by cto")
		d1.CommitAll("chore: rule by dev1")
		cm.Git("push", "-q", "origin", "HEAD:main")
		r := d1.TryGit("pull", "-q", "--no-rebase", "origin", "main")
		if strings.Contains(r.Out(), "CONFLICT") || strings.Contains(d1.Status(), "UU ") {
			t.Fatalf("two rule adds conflicted in a rendered file (D-100):\n%s\n%s", r.Out(), d1.Status())
		}
		dev1.LoreIn(d1, "doctor").Says(t, "open conflicts:   0")
	})

	s.Step("week2/refused-hand-edit-is-not-committed", func() {
		files, err := filepath.Glob(filepath.Join(d1.Dir, ".lore", "data", "projects", "*.json"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no project row file: %v", err)
		}
		b, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, files[0], strings.Replace(string(b), "\n}", ",\n  \"my_note\": \"secret-ish\"\n}", 1))
		d1.Git("add", "-A", ".lore")
		r := d1.TryGit("commit", "-q", "-m", "chore: odd hand edit")
		if r.Code == 0 || !strings.Contains(r.Out(), "unknown field my_note") {
			t.Errorf("a refused hand edit was committed (D-97):\n%s", r)
		}
		d1.Git("reset", "-q", "HEAD", "--", ".lore")
		writeFile(t, files[0], string(b))
	})

	s.Step("week2/the-tool-nobody-inits-stays-byte-identical", func() {
		before := dSnap(s, all...)
		hooks := filepath.Join(cto.Home, ".githooks")
		writeFile(t, filepath.Join(hooks, "pre-commit"), "#!/bin/sh\n# the hook manager's own check\nexit 0\n")
		if err := os.Chmod(filepath.Join(hooks, "pre-commit"), 0o755); err != nil {
			t.Fatal(err)
		}
		ct.Git("config", "--global", "core.hooksPath", hooks)
		// lore wires the shared folder from a lore repo; the tool must not notice.
		cto.LoreIn(cm, "task", "add", "--title", "wire the shared hooks", "--commitment", "accepted").OK(t)
		toolTree = TreeOf(t, ct.Dir)
		cto.LoreIn(ct, "setup", "--agent-hooks", "--dry-run").OK(t)
		s.Untouched(t, ct.Dir, toolTree, "lore setup --agent-hooks --dry-run")
		cto.LoreIn(ct, "task", "list")
		cto.LoreIn(ct, "doctor")
		cto.LoreIn(ct, "render", "--check")
		cto.LoreIn(ct, "search", "--query", "cents")
		s.Untouched(t, ct.Dir, toolTree, "reads, doctor and render --check")
		ct.WriteFile("main.go", "package main\n\n// tweak\n")
		r := ct.TryGit("commit", "-qam", "chore: tweak")
		if r.Code != 0 || strings.Contains(r.Out(), "lore") {
			t.Errorf("a commit in the tool ran lore (exit %d):\n%s", r.Code, r.Out())
		}
		ct.Git("reset", "-q", "--hard", "HEAD~1")
		s.Untouched(t, ct.Dir, toolTree, "a commit through the shared hooks")
		ct.Git("config", "--global", "--unset", "core.hooksPath")
		dNoPush(t, s, before, "commands in the tool")
	})
}

// trailerLines keeps the "Key: value" trailer lines of a commit message, as a
// squash-merge tool copies them into the squashed commit.
func trailerLines(msg string) string {
	var keep []string
	for _, l := range strings.Split(msg, "\n") {
		if strings.HasPrefix(l, "Lore-") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}
