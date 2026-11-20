//go:build journeys && !windows

package journeys

// untracked_files_test.go — devco D-732: lore never leaves an untracked
// file in a repo. Seen in a repo whose core.hooksPath is a committed
// .githooks (the team's own pre-push gate): lore created .githooks/
// commit-msg and .githooks/post-commit, and wrote its agent hooks to
// .claude/settings.json; every `git status` listed them, one `git add -A`
// from reaching the team. Now nothing lore writes shows in git status
// (only .lore/ rows wait for a commit), doctor prints the lines to paste
// into the repo's own hooks, doctor --fix removes the lore-only files an
// older lore left (a file with the person's own lines stays), the agent
// hooks go to .claude/settings.local.json, and only the team's opt-in
// (context.hooks.agent_hooks shared) writes .claude/settings.json.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// strayStatus is git status without the .lore/ rows lore owns in an own
// repo: anything left is a file lore had no business leaving.
func strayStatus(c *teamharness.Clone) string {
	var out []string
	for _, l := range strings.Split(c.Git("status", "--porcelain", "--untracked-files=all"), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.Contains(l, ".lore/") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestJourneyNoUntrackedFilesTrackedHooks(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	app := w.Remote("github.com/orchard-dev/gate")
	ida := w.Persona("Ida Marsh", "ida@orchard.example").Laptop()
	var c *teamharness.Clone
	const gate = "#!/bin/sh\necho team gate\n"

	w.Step("day1/a-repo-with-its-own-committed-hooks-folder", func() {
		c = ida.Clone(app, "gate")
		c.WriteFile(".githooks/pre-push", gate)
		c.WriteFile(".claude/commands/review.md", "review the diff\n")
		c.Git("config", "core.hooksPath", ".githooks")
		c.CommitAll("the team's hooks and the claude folder")
		ida.LoreIn(c, "init", "--target", "claude_code").OK(t)
		c.CommitAll("lore")
		c.Git("push", "-q", "origin", "HEAD")
	})

	w.Step("day1/sync-doctor-fix-and-agent-hooks-leave-nothing-untracked", func() {
		ida.LoreIn(c, "sync")
		ida.LoreIn(c, "doctor", "--fix")
		ida.LoreIn(c, "setup", "--agent-hooks", "--yes").OK(t).Says(t, ".claude/settings.local.json")
		if s := strayStatus(c); s != "" {
			t.Fatalf("lore left files in git status:\n%s", s)
		}
		if got, _ := c.ReadFile(".githooks/pre-push"); got != gate {
			t.Fatalf("the team's pre-push changed:\n%s", got)
		}
		if _, ok := c.ReadFile(".claude/settings.json"); ok {
			t.Fatal("lore wrote .claude/settings.json without the team's opt-in")
		}
		if got, _ := c.ReadFile(".claude/settings.local.json"); !strings.Contains(got, "lore agent start") {
			t.Fatalf("the agent hooks are not in settings.local.json:\n%s", got)
		}
	})

	w.Step("day1/doctor-prints-the-lines-to-paste", func() {
		r := ida.LoreIn(c, "doctor")
		r.Says(t, "lore writes nothing into", "paste into", ".githooks/pre-commit", "# >>> lore-sync >>>")
	})

	w.Step("day2/an-older-lore's-files-are-removed-a-mixed-one-stays", func() {
		c.WriteFile(".githooks/commit-msg", "#!/bin/sh\n# >>> lore-sync >>>\nlore task hook commit-msg \"$1\" || true\n# <<< lore-sync <<<\n")
		mixed := "#!/bin/sh\necho mine\n# >>> lore-sync:live >>>\nlore sync hook post-commit || true\n# <<< lore-sync:live <<<\n"
		c.WriteFile(".githooks/post-commit", mixed)
		ida.LoreIn(c, "doctor").Says(t, "untracked file(s) commit-msg", "`lore doctor --fix` removes them")
		ida.LoreIn(c, "doctor", "--fix").Says(t, "removed untracked file(s) commit-msg")
		if _, ok := c.ReadFile(".githooks/commit-msg"); ok {
			t.Fatal("doctor --fix kept the lore-only commit-msg")
		}
		if got, _ := c.ReadFile(".githooks/post-commit"); got != mixed {
			t.Fatalf("doctor --fix changed the person's post-commit:\n%s", got)
		}
	})

	w.Step("day2/the-team-opts-in-to-shared-agent-hooks", func() {
		ida.LoreIn(c, "policy", "set", "--key", "context.hooks.agent_hooks", "--value", "shared", "--yes").OK(t)
		ida.LoreIn(c, "setup", "--agent-hooks", "--yes").OK(t).Says(t, "move SessionStart hook from .claude/settings.local.json")
		if got, _ := c.ReadFile(".claude/settings.json"); !strings.Contains(got, "lore agent start") {
			t.Fatalf("shared: the agent hooks are not in settings.json:\n%s", got)
		}
	})
}

// TestJourneyNoUntrackedFilesLinkedShapes (devco D-732, the standing
// rule): in a client repo linked with no .lore/, a client repo whose hooks
// folder is committed, and an own repo linked with no .lore/, every set-up
// command (link, sync, setup, doctor --fix) leaves `git status` empty and
// the committed hooks folder byte for byte as it was.
func TestJourneyNoUntrackedFilesLinkedShapes(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	const gate = "#!/bin/sh\necho client gate\n"

	w.Step("day1/team-and-folders", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		for _, f := range []string{"client_plain", "client_hooks", "own_tools"} {
			lee.Lore(parent, "store", "folder", "add", "team", f).OK(t)
		}
		lee.Lore(parent, "org", "config", "set", "--org", tlOrg, "--key", "own", "--add", "github.com/orchard-dev/*", "--yes").OK(t)
	})

	for _, shape := range []struct {
		name, slug, folder string
		trackedHooks, own  bool
	}{
		{"client-repo", "github.com/client-co/plain", "client_plain", false, false},
		{"client-repo-with-committed-hooks", "github.com/client-co/hooked", "client_hooks", true, false},
		{"own-repo-linked-no-lore", "github.com/orchard-dev/tools", "own_tools", false, true},
	} {
		w.Step("day1/"+shape.name, func() {
			c := jPlainRepo(t, lee, w.Remote(shape.slug), filepath.Join(parent, filepath.Base(shape.slug)))
			if shape.trackedHooks {
				c.WriteFile(".githooks/pre-push", gate)
				c.CommitAll("the client's own hooks")
				c.Git("config", "core.hooksPath", ".githooks")
			}
			link := []string{"org", "link", tlOrg, "--project", shape.folder, "--yes"}
			if shape.own {
				link = append(link, "--own")
			}
			r := lee.LoreIn(c, link...).OK(t)
			if shape.trackedHooks {
				r.Says(t, "a folder committed with the repo", "lore never changes a client repo's files", "check leaks --staged --commit-msg")
			}
			lee.LoreIn(c, "sync")
			lee.LoreIn(c, "setup")
			lee.LoreIn(c, "doctor", "--fix")
			if st := c.Git("status", "--porcelain", "--untracked-files=all"); st != "" {
				t.Fatalf("%s: lore left files in git status:\n%s", shape.name, st)
			}
			if shape.trackedHooks {
				// whoami's guard step is doctor's paste lines, and doctor prints them
				lee.LoreIn(c, "whoami").OK(t).Says(t, "guards OFF in this clone", "`lore doctor` prints the lines to paste into its commit-msg").Never(t, "lore org link --org")
				lee.LoreIn(c, "doctor").Says(t, "a folder committed with the repo", "check leaks --staged --commit-msg")
				if got, _ := c.ReadFile(".githooks/pre-push"); got != gate {
					t.Fatalf("the client's pre-push changed:\n%s", got)
				}
			}
		})
	}
}
