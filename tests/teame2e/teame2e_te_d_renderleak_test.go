//go:build teame2e

package teame2e

// later.md 259 (F20, T-TEAME2E): after `lore render` in a client repo, for
// every tool profile the repo can target, nothing internal or personal is
// anywhere in the checkout (tracked, untracked, ignored, .git files, git
// objects), nor anywhere the client can fetch after the render is
// committed and pushed. The internal rows still reach the agent through
// the profile's private file, which lives outside the checkout (an import
// line) or not at all.

import (
	"os"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teDPersonalNote is a personal note (the `me` store).
const teDPersonalNote = "I always forget to renew the staging VPN certificate on Mondays"

// teDInternalTask is an internal task title (the org store, acme folder).
const teDInternalTask = "chase the overdue invoice before the next demo call"

// teDRenderTargets is every tool profile name a repo can target
// (logic/toolprofile), each with the private file it writes inside the
// checkout ("" = none: its internal context comes through lore context).
var teDRenderTargets = []struct {
	target  string
	private string
}{
	{"claude_code", "CLAUDE.local.md"},
	{"codex", ""},
	{"cursor", ".cursor/rules/lore-private.mdc"},
	{"gemini_cli", ""},
	{"copilot", ""},
	{"generic", ""},
}

func TestTeD_RenderLeakScanEveryTarget(t *testing.T) {
	for _, tc := range teDRenderTargets {
		t.Run(tc.target, func(t *testing.T) {
			a := newAgency(t, th.Flag("to", "memory", "add"), th.Flag("to", "rule", "add"), th.Flag("to", "task", "add"))
			memID := mustID(t, a.aliceApp.MustLore("memory", "add", "--body", teDInternalNote), "mem")
			a.aliceApp.MustLore("memory", "add", "--body", teDPersonalNote, "--to", "me")
			taskID := mustID(t, a.aliceApp.MustLore("task", "add", teDInternalTask, "--to", orgNorthwind), "tsk")
			a.aliceApp.MustLore("rule", "add", "--body", "money is stored in minor units", "--to", "repo")
			a.aliceApp.MustLore("policy", "set", "context.targets", tc.target, "--to", "repo", "--yes")
			a.aliceApp.MustLore("render")

			// The committed context keeps the repo's own rows.
			if md, _ := a.aliceApp.ReadFile(".lore/LORE.md"); !strings.Contains(md, "money is stored in minor units") {
				t.Fatalf("LORE.md keeps the repo's rule: %q", md)
			}
			// The private file, when the profile has one, is never
			// tracked and is excluded through .git/info/exclude only.
			if tc.private != "" {
				if _, ok := a.aliceApp.ReadFile(tc.private); !ok {
					t.Fatalf("%s writes its private file %s", tc.target, tc.private)
				}
				if out := a.aliceApp.Git("ls-files", tc.private); out != "" {
					t.Fatalf("%s must never be tracked", tc.private)
				}
				if ex, _ := a.aliceApp.ReadFile(".git/info/exclude"); !strings.Contains(ex, tc.private) {
					t.Fatalf("%s is excluded through .git/info/exclude: %q", tc.private, ex)
				}
			}
			if gi, _ := a.aliceApp.ReadFile(".gitignore"); strings.Contains(gi, "local.md") || strings.Contains(gi, "lore-private") {
				t.Fatalf("the client's .gitignore never names a private file: %q", gi)
			}
			needles := []string{teDInternalNote, teDPersonalNote, teDInternalTask, memID, taskID, slugStore, aliceKey}
			if f := a.aliceApp.LeakScan(needles...); len(f) != 0 {
				t.Fatalf("%s: internal or personal content inside the client checkout after render: %v", tc.target, f)
			}

			// Committed and pushed, then the client pulls and renders too:
			// nothing internal anywhere the client can fetch.
			a.aliceApp.CommitAll("render")
			a.aliceApp.Git("push", "-q", "origin", "main")
			a.aliceApp.MustLore("sync")
			a.acmeApp.Git("pull", "-q")
			a.acmeApp.MustLore("render")
			a.acmeApp.MustLore("rule", "list").Says(t, "money is stored in minor units")
			if f := a.clientSees(t, needles...); len(f) != 0 {
				t.Fatalf("%s: the client can fetch internal or personal content: %v", tc.target, f)
			}
			// Control: the scan finds what it looks for (a needle the
			// repo does hold), so an empty result above means something.
			if f := a.aliceApp.LeakScan("money is stored in minor units"); len(f) == 0 {
				t.Fatal("control: the leak scan must find the repo's own rule")
			}
			if _, err := os.Stat(a.alice.LorePath("render")); tc.private != "" && err != nil {
				t.Fatalf("%s keeps its private render under ~/.lore/render: %v", tc.target, err)
			}
		})
	}
}
