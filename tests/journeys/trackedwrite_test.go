//go:build journeys && !windows

package journeys

// trackedwrite_test.go — lore never writes into a repo's tracked files
// where it may not (devco D-500, D-501).
//
//   - D-500: a code repo whose .gitignore lists .lore/ (a person keeps
//     lore private to this machine), with tracked AGENTS.md, CLAUDE.md,
//     .gitattributes and a tracked hooks folder as core.hooksPath. init,
//     a write, render, sync and doctor --fix wrote a pointer block into
//     AGENTS.md and CLAUDE.md, lore's lines into .gitattributes and hook
//     blocks into .githooks. Now `git status` stays clean and every
//     tracked file byte-identical; the pointer goes into the git-excluded
//     CLAUDE.local.md and one line says why.
//   - D-501: a repo that commits .lore/ with a tracked core.hooksPath:
//     init, doctor --fix and `lore fix --yes` added the lore-sync:live
//     block (and every other) to .githooks without asking. Now they leave
//     it alone and name `lore fix --tracked-hooks`, which writes them.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// twTeamFiles are the tracked files of the journey's code repo.
var twTeamFiles = map[string]string{
	"AGENTS.md":          "# Agents\n\nrules here\n",
	"CLAUDE.md":          "# Claude\n\nnotes\n",
	".gitattributes":     "*.sh text eol=lf\n",
	".githooks/pre-push": "#!/bin/sh\necho team pre-push\n",
	"main.go":            "package main\n",
}

// twRepo pushes a code repo holding twTeamFiles and ignore as .gitignore,
// with git's hooks in the tracked .githooks.
func twRepo(t *testing.T, w *World, lap *Machine, name, ignore string) *teamharness.Clone {
	t.Helper()
	c := lap.Clone(w.Remote("github.com/gadgets/"+name), name)
	c.WriteFile(".gitignore", ignore)
	for f, body := range twTeamFiles {
		c.WriteFile(f, body)
	}
	if err := os.Chmod(filepath.Join(c.Dir, ".githooks", "pre-push"), 0o755); err != nil { // #nosec G302 -- a hook git must run
		t.Fatal(err)
	}
	c.Git("add", "-A")
	c.TryGit("commit", "-q", "-m", "start").OK(t)
	c.Git("config", "core.hooksPath", ".githooks")
	return c
}

// twUntouched fails when lore changed a tracked file or left any file git
// would show (git status --porcelain --untracked-files=all).
func twUntouched(t *testing.T, c *teamharness.Clone, after string) {
	t.Helper()
	if st := c.Git("status", "--porcelain", "--untracked-files=all"); strings.TrimSpace(st) != "" {
		t.Fatalf("%s: lore changed the repo's files:\n%s", after, st)
	}
	for f, body := range twTeamFiles {
		if b, err := os.ReadFile(filepath.Join(c.Dir, filepath.FromSlash(f))); err != nil || string(b) != body {
			t.Fatalf("%s: tracked %s changed (%v):\n%s", after, f, err, b)
		}
	}
}

func TestJourneyPrivateProjectWritesNoTrackedFile(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kim := w.Persona("Kim Ardent", "kim@gadgets.example")
	lap := kim.Laptop()
	c := twRepo(t, w, lap, "private", "bin/\n.lore/\n")

	w.Step("day1/init-in-a-repo-that-ignores-lore", func() {
		lap.LoreIn(c, "init").OK(t).Says(t, "private to this machine", "CLAUDE.local.md")
		twUntouched(t, c, "lore init")
	})

	w.Step("day1/write-render-sync-doctor-fix-touch-nothing-tracked", func() {
		lap.LoreIn(c, "memory", "add", "--body", "flash the board twice").OK(t)
		twUntouched(t, c, "a write")
		lap.LoreIn(c, "render").OK(t).Says(t, "AGENTS.md", "pointer skipped")
		twUntouched(t, c, "lore render")
		lap.LoreIn(c, "sync") // the rows stay on this machine; it may refuse, it must not write
		twUntouched(t, c, "lore sync")
		lap.LoreIn(c, "doctor", "--fix")
		twUntouched(t, c, "lore doctor --fix")
		lap.LoreIn(c, "fix", "--yes", "--tracked-hooks")
		twUntouched(t, c, "lore fix --tracked-hooks")
	})

	w.Step("day1/the-agent-still-gets-lore-md", func() {
		b, err := os.ReadFile(filepath.Join(c.Dir, "CLAUDE.local.md"))
		if err != nil || !strings.Contains(string(b), "@.lore/LORE.md") {
			t.Fatalf("CLAUDE.local.md does not import .lore/LORE.md (%v):\n%s", err, b)
		}
		if _, err := os.Stat(filepath.Join(c.Dir, ".lore", "LORE.md")); err != nil {
			t.Fatalf("no .lore/LORE.md rendered: %v", err)
		}
	})
}

func TestJourneyTrackedHooksFolderNeedsTheFlag(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kim := w.Persona("Kim Ardent", "kim@gadgets.example")
	lap := kim.Laptop()
	c := twRepo(t, w, lap, "shared", "bin/\n")
	hooks := filepath.Join(c.Dir, ".githooks")
	hooksUntouched := func(after string) {
		t.Helper()
		if st := c.Git("status", "--porcelain", "--untracked-files=all", "--", ".githooks"); st != "" {
			t.Fatalf("%s: lore created or changed files in the tracked .githooks:\n%s", after, st)
		}
	}

	w.Step("day1/init-and-fix-leave-the-tracked-hooks-alone", func() {
		lap.LoreIn(c, "init").OK(t).Says(t, "lore did not create", hooks, "`lore fix --tracked-hooks`")
		hooksUntouched("lore init")
		lap.LoreIn(c, "doctor", "--fix").Says(t, "`lore fix --tracked-hooks`")
		hooksUntouched("lore doctor --fix")
		lap.LoreIn(c, "fix", "--yes").Says(t, "`lore fix --tracked-hooks`")
		hooksUntouched("lore fix --yes")
	})

	w.Step("day1/the-explicit-yes-writes-them", func() {
		lap.LoreIn(c, "fix", "--yes", "--tracked-hooks").Says(t, "hook blocks in "+hooks, "a folder committed with the repo")
		b, err := os.ReadFile(filepath.Join(hooks, "pre-push"))
		if err != nil || !strings.Contains(string(b), "lore-sync:live") || !strings.Contains(string(b), "echo team pre-push") {
			t.Fatalf("lore fix --tracked-hooks did not chain its block into the team's pre-push (%v):\n%s", err, b)
		}
	})
}
