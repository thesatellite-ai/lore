//go:build journeys && !windows

package journeys

// clientrepo_prepush_addedlines_test.go — devco:D-728: the pre-push leak
// check reads only the lines the pushed commits add, as the commit check
// does since D-720. A client repo's CLAUDE.md held hand-written text,
// committed and pushed long before that text became a note in the store;
// a commit adding an unrelated line passed the commit hook, and then the
// push was refused for the old lines. Now the push goes out, and a pushed
// commit that adds a line repeating the note (committed with --no-verify,
// so only the pre-push hook sees it) is still refused, naming CLAUDE.md and
// the line.

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	ppSlug   = "github.com/client-co/payroll"
	ppFolder = "client_payroll"
	ppRule   = "Always reconcile payroll exports against the nightly settlement batch first."
	ppHook   = "#!/bin/sh\nif command -v lore >/dev/null 2>&1 && lore land --help >/dev/null 2>&1; then lore sync hook pre-push \"$@\" || exit $?; fi\n"
)

func TestJourneyClientRepoPrePushAddedLines(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	remote := w.Remote(ppSlug)
	claudeMD := "# Payroll\n\n" + ppRule + "\n"

	w.Step("day1/push-of-an-unrelated-line-goes-out-a-pushed-leaking-line-refused", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", ppFolder).OK(t)
		c := jPlainRepo(t, lee, remote, filepath.Join(parent, "payroll"))
		// the hand-written CLAUDE.md is committed and pushed before lore is linked
		jWrite(t, filepath.Join(c.Dir, "CLAUDE.md"), claudeMD)
		c.Git("add", "CLAUDE.md")
		c.Git("commit", "-q", "-m", "Add CLAUDE.md")
		c.Git("push", "-q", "origin", "HEAD")
		lee.LoreIn(c, "org", "link", tlOrg, "--project", ppFolder, "--yes").OK(t)
		lee.LoreIn(c, "rule", "add", "--body", ppRule, "--severity", "must").OK(t).Says(t, ppFolder)
		// lore's pre-push block, as a clone lore wired before holds it
		// (the link itself installs only the commit-msg block)
		jWrite(t, filepath.Join(c.Dir, ".git", "hooks", "pre-push"), ppHook)
		if err := os.Chmod(filepath.Join(c.Dir, ".git", "hooks", "pre-push"), 0o755); err != nil {
			t.Fatal(err)
		}

		// an unrelated edit commits and pushes through the real hooks
		jWrite(t, filepath.Join(c.Dir, "CLAUDE.md"), claudeMD+"\n## Build\n\nRun make test before a pull request.\n")
		c.Git("add", "CLAUDE.md")
		if r := c.TryGit("commit", "-q", "-m", "Document the build"); r.Code != 0 {
			t.Fatalf("an edit adding unrelated lines to a pushed CLAUDE.md was refused at commit: %s", r)
		}
		if r := c.TryGit("push", "-q", "origin", "HEAD"); r.Code != 0 {
			t.Fatalf("a push adding unrelated lines to a pushed CLAUDE.md was refused for its old text: %s", r)
		}

		// a pushed line that repeats the note is refused, naming file:line
		body, err := os.ReadFile(filepath.Join(c.Dir, "CLAUDE.md"))
		if err != nil {
			t.Fatal(err)
		}
		jWrite(t, filepath.Join(c.Dir, "CLAUDE.md"), string(body)+"\nNote: "+ppRule+"\n")
		c.Git("add", "CLAUDE.md")
		c.Git("commit", "-q", "--no-verify", "-m", "Repeat the rule")
		r := c.TryGit("push", "-q", "origin", "HEAD")
		if r.Code == 0 {
			t.Fatalf("a pushed line repeating an internal note went out: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED", "CLAUDE.md:9 ").Never(t, "CLAUDE.md:3 ")
	})
}
