//go:build journeys && !windows

package journeys

// nolore_globalhooks_test.go — the untouched-repo rule with a hook manager
// (D-20, D-21). Kai keeps every repo's git hooks in one folder
// (core.hooksPath in the global git config, a manager's own pre-commit and
// commit-msg ending in `exit 0`). One repo is a lore project; the other,
// a config repo, has no lore data and nobody ever ran lore init in it.
//
// Before: `lore setup --agent-hooks` (even --dry-run) made an empty .lore/
// in the config repo, so the next `lore sync` wired git into it; and lore's
// hook blocks in the shared folder ran lore on every commit of the config
// repo, which then gained lore.clientrepo, a refs/lore fetch refspec, an
// empty .lore/ and .git/lore files. Now every command anyone runs there,
// and a commit through the shared hooks, leave its tree, .git/config,
// refs and `git status --ignored` byte-identical, while the lore project's
// commits still run lore's hooks.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyNoLoreRepoGlobalHooksPath(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kai := w.Persona("Kai Brandt", "kai@hooks.example")
	lap := kai.Laptop()
	var app, cfg *teamharness.Clone
	hooks := filepath.Join(lap.Home, ".githooks")
	const managerLine = "# the hook manager's own check"

	w.Step("day1/a-hook-manager-owns-the-global-hooks-folder", func() {
		for _, h := range []string{"pre-commit", "commit-msg"} {
			jWrite(t, filepath.Join(hooks, h), "#!/bin/sh\n"+managerLine+"\nexit 0\n")
			if err := os.Chmod(filepath.Join(hooks, h), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		app = jPlainRepo(t, lap, w.Remote("github.com/kai-dev/app"), filepath.Join(lap.Home, "code", "app"))
		app.Git("config", "--global", "core.hooksPath", hooks)
		cfg = jPlainRepo(t, lap, w.Remote("github.com/kai-dev/cfgkit"), filepath.Join(lap.Home, "code", "cfgkit"))
	})

	w.Step("day1/the-lore-project-wires-the-shared-folder-with-guards", func() {
		lap.LoreIn(app, "init").OK(t)
		lap.LoreIn(app, "task", "add", "--title", "first task").OK(t)
		lap.LoreIn(app, "sync").OK(t)
		for _, h := range []string{"pre-commit", "commit-msg", "post-commit", "pre-push"} {
			b, err := os.ReadFile(filepath.Join(hooks, h))
			if err != nil {
				t.Fatalf("no %s in the shared hooks folder: %v", h, err)
			}
			body := string(b)
			if !strings.Contains(body, "lore") {
				t.Fatalf("%s holds no lore block:\n%s", h, body)
			}
			for _, l := range strings.Split(body, "\n") {
				if strings.Contains(l, "lore ") && !strings.HasPrefix(l, "#") && !strings.HasPrefix(l, "if [ ") {
					t.Fatalf("%s: a lore line in the shared folder runs in every repo (no guard):\n%s", h, l)
				}
			}
			if (h == "pre-commit" || h == "commit-msg") && !strings.Contains(body, managerLine) {
				t.Fatalf("%s lost the hook manager's own lines:\n%s", h, body)
			}
		}
	})

	w.Step("day2/every-command-and-a-commit-leave-the-config-repo-alone", func() {
		w.AdvanceDays(1)
		before := nlTreeHash(t, cfg.Dir)
		snap := func() string {
			return cfg.Git("status", "--porcelain", "--ignored") + "\n" + cfg.Git("for-each-ref", "--format=%(refname)")
		}
		gitBefore := snap()
		lap.LoreIn(cfg, "setup", "--agent-hooks", "--dry-run").OK(t)
		nlSame(t, "lore setup --agent-hooks --dry-run", cfg.Dir, before)
		lap.LoreIn(cfg, "setup", "--agent-hooks", "--yes").OK(t)
		nlSame(t, "lore setup --agent-hooks --yes", cfg.Dir, before)
		lap.LoreIn(cfg, "setup", "--schema-only")
		nlSame(t, "lore setup --schema-only", cfg.Dir, before)
		lap.LoreIn(cfg, "sync")
		nlSame(t, "lore sync", cfg.Dir, before)
		lap.LoreIn(cfg, "task", "list").Fails(t, 19, "lore isn't set up")
		lap.LoreIn(cfg, "doctor")
		lap.LoreIn(cfg, "search", "--query", "first")
		lap.LoreIn(cfg, "task", "add", "--title", "should not land here")
		nlSame(t, "lore task list, doctor, search, task add", cfg.Dir, before)

		cfg.WriteFile("main.go", "package main\n\n// a tweak\n")
		r := cfg.TryGit("commit", "-qam", "chore: tweak")
		if r.Code != 0 || strings.Contains(r.Out(), "lore") {
			t.Fatalf("a commit in a repo with no lore data ran lore (exit %d):\n%s", r.Code, r.Out())
		}
		cfg.Git("reset", "-q", "--hard", "HEAD~1")
		nlSame(t, "git commit through the shared hooks", cfg.Dir, before)
		if got := snap(); got != gitBefore {
			t.Fatalf("status or refs of the config repo changed:\nbefore:\n%s\nafter:\n%s", gitBefore, got)
		}
		for _, p := range []string{".lore", filepath.Join(".git", "lore")} {
			if _, err := os.Stat(filepath.Join(cfg.Dir, p)); err == nil {
				t.Fatalf("%s appeared in a repo with no lore data", p)
			}
		}
	})

	w.Step("day2/the-lore-project-still-runs-its-hooks", func() {
		app.WriteFile("main.go", "package main\n\n// v2\n")
		app.CommitAll("feat: v2")
		if got := app.Git("log", "-1", "--format=%s"); got != "feat: v2" {
			t.Fatalf("the lore project's commit did not land: %q", got)
		}
		if out := app.Git("ls-files", ".lore/data"); out == "" {
			t.Fatal("the lore project's pre-commit did not add its row files: the shared hooks skipped a lore repo")
		}
	})
}
