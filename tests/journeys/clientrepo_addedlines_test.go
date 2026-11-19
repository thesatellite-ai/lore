//go:build journeys && !windows

package journeys

// clientrepo_addedlines_test.go — devco:D-720: the client-repo leak guard
// reads only the lines a commit adds. A client repo's CLAUDE.md was
// committed with hand-written text before that text became a note in the
// store; afterwards every edit of the file was refused for the old text.
// Now an edit that adds unrelated lines commits through the real hook,
// and an added line that repeats the note is still refused, naming
// CLAUDE.md and the line.

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	alSlug   = "github.com/client-co/ledger"
	alFolder = "client_ledger"
	alRule   = "Always reconcile ledger exports against the nightly settlement batch first."
)

func TestJourneyClientRepoLeakAddedLines(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	remote := w.Remote(alSlug)
	claudeMD := "# Ledger\n\n" + alRule + "\n"

	w.Step("day1/old-text-stays-new-lines-commit-a-leaking-line-refused", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", alFolder).OK(t)
		c := jPlainRepo(t, lee, remote, filepath.Join(parent, "ledger"))
		// the hand-written CLAUDE.md is committed and pushed before lore
		// is linked (unpushed commits count as part of the next commit)
		jWrite(t, filepath.Join(c.Dir, "CLAUDE.md"), claudeMD)
		c.Git("add", "CLAUDE.md")
		c.Git("commit", "-q", "-m", "Add CLAUDE.md")
		c.Git("push", "-q", "origin", "HEAD")
		lee.LoreIn(c, "org", "link", tlOrg, "--project", alFolder, "--yes").OK(t)
		lee.LoreIn(c, "rule", "add", "--body", alRule, "--severity", "must").OK(t).Says(t, alFolder)

		// an unrelated edit of the committed file goes through the hook
		jWrite(t, filepath.Join(c.Dir, "CLAUDE.md"), claudeMD+"\n## Build\n\nRun make test before a pull request.\n")
		c.Git("add", "CLAUDE.md")
		if r := c.TryGit("commit", "-q", "-m", "Document the build"); r.Code != 0 {
			t.Fatalf("an edit adding unrelated lines to a committed CLAUDE.md was refused for its old text: %s", r)
		}

		// an added line that repeats the note is refused, naming file:line
		body, err := os.ReadFile(filepath.Join(c.Dir, "CLAUDE.md"))
		if err != nil {
			t.Fatal(err)
		}
		jWrite(t, filepath.Join(c.Dir, "CLAUDE.md"), string(body)+"\nNote: "+alRule+"\n")
		c.Git("add", "CLAUDE.md")
		r := c.TryGit("commit", "-q", "-m", "Repeat the rule")
		if r.Code == 0 {
			t.Fatalf("an added line repeating an internal note was committed: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED", "CLAUDE.md:9 ").Never(t, "CLAUDE.md:3 ")
	})
}
