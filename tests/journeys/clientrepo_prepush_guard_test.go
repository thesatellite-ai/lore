//go:build journeys && !windows

package journeys

// clientrepo_prepush_guard_test.go — devco:D-730: a client repo a store
// links by its remote, with no .lore/, got only lore's commit-msg block
// from `lore org link`, so a commit made with --no-verify was never
// checked before it left the clone. Now the link installs the pre-push
// block too, in .git/hooks only (the repo's tracked files and git status
// stay byte-identical), `lore doctor` warns when a block is missing and
// `lore doctor --fix` installs it, and a repo whose hooks folder is
// committed with the repo (core.hooksPath=.githooks) is never written:
// the link and doctor print the exact lines to add, which run as printed.

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	pgSlug        = "github.com/client-co/ledger"
	pgTrackedSlug = "github.com/client-co/billing"
	pgFolder      = "client_ledger"
	pgRule        = "Always rotate the ledger signing keys before the quarterly audit window opens."
	pgCommitLine  = `lore check leaks --staged --commit-msg "$1" || exit $?`
	pgPushLine    = `lore sync hook pre-push "$@" || exit $?`
	pgClientHook  = "#!/bin/sh\necho client-hook-ran >/dev/null\n"
)

// pgSnapshot is what must not change in a client repo: git status, the
// index's tracked entries and the bytes of every tracked file.
func pgSnapshot(t *testing.T, dir string, c interface{ Git(...string) string }) string {
	t.Helper()
	out := c.Git("status", "--porcelain", "--untracked-files=all") + "\n--\n" + c.Git("ls-files", "-s")
	for _, f := range []string{"CLAUDE.md", ".githooks/commit-msg", ".githooks/pre-push"} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
			out += "\n--" + f + "\n" + string(b)
		}
	}
	return out
}

func TestJourneyClientRepoPrePushGuard(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	claudeMD := "# Ledger\n\nRead the README first.\n"

	w.Step("day1/link-installs-pre-push-in-git-hooks-only-a-no-verify-leak-is-refused-at-push", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", pgFolder).OK(t)
		c := jPlainRepo(t, lee, w.Remote(pgSlug), filepath.Join(parent, "ledger"))
		jWrite(t, filepath.Join(c.Dir, "CLAUDE.md"), claudeMD)
		c.Git("add", "CLAUDE.md")
		c.Git("commit", "-q", "-m", "Add CLAUDE.md")
		c.Git("push", "-q", "origin", "HEAD")

		before := pgSnapshot(t, c.Dir, c)
		lee.LoreIn(c, "org", "link", tlOrg, "--project", pgFolder, "--yes").OK(t).Says(t, filepath.Join(".git", "hooks", "pre-push"))
		if after := pgSnapshot(t, c.Dir, c); after != before {
			t.Fatalf("the link changed the client repo's files or git status:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		for _, h := range []string{"commit-msg", "pre-push"} {
			if b, err := os.ReadFile(filepath.Join(c.Dir, ".git", "hooks", h)); err != nil || len(b) == 0 {
				t.Fatalf("the link left no %s hook in .git/hooks: %v", h, err)
			}
		}
		lee.LoreIn(c, "rule", "add", "--body", pgRule, "--severity", "must").OK(t)
		lee.LoreIn(c, "doctor").OK(t).Says(t, "client repo leak guard installed").Never(t, "NOT checked")

		// an unrelated push passes through the real hooks
		jWrite(t, filepath.Join(c.Dir, "CLAUDE.md"), claudeMD+"\n## Build\n\nRun make test before a pull request.\n")
		c.Git("add", "CLAUDE.md")
		if r := c.TryGit("commit", "-q", "-m", "Document the build"); r.Code != 0 {
			t.Fatalf("an unrelated commit was refused: %s", r)
		}
		if r := c.TryGit("push", "-q", "origin", "HEAD"); r.Code != 0 {
			t.Fatalf("an unrelated push was refused: %s", r)
		}

		// a --no-verify commit adding the note is refused at push, naming file:line
		body, err := os.ReadFile(filepath.Join(c.Dir, "CLAUDE.md"))
		if err != nil {
			t.Fatal(err)
		}
		jWrite(t, filepath.Join(c.Dir, "CLAUDE.md"), string(body)+"\nNote: "+pgRule+"\n")
		c.Git("add", "CLAUDE.md")
		c.Git("commit", "-q", "--no-verify", "-m", "Repeat the rule")
		r := c.TryGit("push", "-q", "origin", "HEAD")
		if r.Code == 0 {
			t.Fatalf("a --no-verify commit quoting an internal note was pushed: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED", "CLAUDE.md:9 ")
		c.Git("reset", "-q", "--hard", "HEAD~1")

		// a clone linked before D-730 holds only the commit-msg block:
		// doctor warns and names the step, doctor --fix installs it
		if err := os.Remove(filepath.Join(c.Dir, ".git", "hooks", "pre-push")); err != nil {
			t.Fatal(err)
		}
		before = pgSnapshot(t, c.Dir, c)
		lee.LoreIn(c, "whoami").OK(t).Says(t, "guards on for commits only", "`lore doctor --fix`")
		lee.LoreIn(c, "doctor").Says(t, "client repo leak guard missing in pre-push", "`lore doctor --fix`")
		lee.LoreIn(c, "doctor", "--fix").Says(t, "leak guard installed in this clone")
		lee.LoreIn(c, "doctor").OK(t).Says(t, "client repo leak guard installed")
		lee.LoreIn(c, "whoami").OK(t).Says(t, "guards on").Never(t, "for commits only", "guards OFF")
		if after := pgSnapshot(t, c.Dir, c); after != before {
			t.Fatalf("doctor --fix changed the client repo's files or git status:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	w.Step("day1/tracked-hooks-folder-is-never-written-doctor-prints-lines-that-run-as-printed", func() {
		c := jPlainRepo(t, lee, w.Remote(pgTrackedSlug), filepath.Join(parent, "billing"))
		jWrite(t, filepath.Join(c.Dir, "CLAUDE.md"), claudeMD)
		for _, h := range []string{"commit-msg", "pre-push"} {
			jWrite(t, filepath.Join(c.Dir, ".githooks", h), pgClientHook)
			if err := os.Chmod(filepath.Join(c.Dir, ".githooks", h), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		c.Git("add", "-A")
		c.Git("commit", "-q", "-m", "Add CLAUDE.md and the team hooks")
		c.Git("push", "-q", "origin", "HEAD")
		c.Git("config", "--local", "core.hooksPath", ".githooks")

		before := pgSnapshot(t, c.Dir, c)
		lee.LoreIn(c, "org", "link", tlOrg, "--project", pgFolder, "--yes").OK(t).Says(t, "NOT checked", pgCommitLine, pgPushLine)
		lee.LoreIn(c, "doctor", "--fix").Says(t, "NOT checked", pgCommitLine, pgPushLine).Never(t, "leak guard installed")
		if after := pgSnapshot(t, c.Dir, c); after != before {
			t.Fatalf("lore wrote into a hooks folder committed with the client repo:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		lee.LoreIn(c, "doctor").Says(t, "NOT checked", "committed with the repo", pgCommitLine, pgPushLine)

		// the person adds the printed lines to their hooks: both run as printed
		for h, line := range map[string]string{"commit-msg": pgCommitLine, "pre-push": pgPushLine} {
			jWrite(t, filepath.Join(c.Dir, ".githooks", h), pgClientHook+line+"\n")
		}
		jWrite(t, filepath.Join(c.Dir, "CLAUDE.md"), claudeMD+"\n## Build\n\nRun make test before a pull request.\n")
		c.Git("add", "CLAUDE.md")
		if r := c.TryGit("commit", "-q", "-m", "Document the build"); r.Code != 0 {
			t.Fatalf("an unrelated commit was refused by the printed commit-msg line: %s", r)
		}
		if r := c.TryGit("push", "-q", "origin", "HEAD"); r.Code != 0 {
			t.Fatalf("an unrelated push was refused by the printed pre-push line: %s", r)
		}
		jWrite(t, filepath.Join(c.Dir, "CLAUDE.md"), claudeMD+"\n## Build\n\nRun make test before a pull request.\n\nNote: "+pgRule+"\n")
		c.Git("add", "CLAUDE.md")
		if r := c.TryGit("commit", "-q", "-m", "Repeat the rule"); r.Code == 0 {
			t.Fatalf("the printed commit-msg line let a commit quoting an internal note through: %s", r)
		}
		c.Git("commit", "-q", "--no-verify", "-m", "Repeat the rule")
		r := c.TryGit("push", "-q", "origin", "HEAD")
		if r.Code == 0 {
			t.Fatalf("the printed pre-push line let a commit quoting an internal note out: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED", "CLAUDE.md:")
	})
}
