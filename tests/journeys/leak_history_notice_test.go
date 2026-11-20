//go:build journeys && !windows

package journeys

// leak_history_notice_test.go — a secret-looking value the commit or push
// check lets through because the remote already holds it is named by file,
// line and the ref that holds it (devco:D-788). The notice used to say
// "already in the same row(s) at HEAD" for a plain code file (a Taskfile
// whose test DSN was pushed long ago), naming neither the file nor that
// the reason was the remote. Covers a pushed branch, a commit the remote
// holds only behind a pushed tag, and a squash merge.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyLeakHistoryNamesFileAndRemote(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ines := w.Persona("Ines Ward", "ines@widgets.example")
	lap := ines.Laptop()
	key := "AKIA" + strings.Repeat("H", 16) // built at run time: no key-shaped literal in the repo
	remote := w.Remote("github.com/widgets/histnote")
	var c *teamharness.Clone
	rowWords := "same row(s)"

	w.Step("day1/a-pushed-config-with-a-key", func() {
		c = lap.Clone(remote, "histnote")
		lap.LoreIn(c, "init").OK(t)
		c.WriteFile("cfg/Taskfile.yml", "vars:\n  KEY: "+key+"\n")
		c.Git("add", "-A")
		c.Git("commit", "-q", "--no-verify", "-m", "start")
		c.Git("push", "-q", "--no-verify", "-u", "origin", "HEAD:main")
	})

	w.Step("day1/an-edit-of-that-file-names-file-line-and-branch", func() {
		c.WriteFile("cfg/Taskfile.yml", "vars:\n  KEY: "+key+"\n  PORT: 5432\n")
		c.Git("add", "cfg/Taskfile.yml")
		c.TryGit("commit", "-q", "-m", "port").OK(t).
			Says(t, "cfg/Taskfile.yml:2", "already on the remote in the same file (origin/main)", "does not block this commit").Never(t, key, rowWords)
		c.TryGit("push", "-q", "origin", "HEAD:main").OK(t).
			Says(t, "cfg/Taskfile.yml:2", "(origin/main)", "does not block this push").Never(t, key, rowWords)
	})

	w.Step("day1/a-value-the-remote-holds-behind-a-tag-names-the-tag", func() {
		c.Git("checkout", "-q", "-b", "rel")
		c.WriteFile("rel/env.yml", "KEY: "+key+"\n")
		c.Git("add", "rel/env.yml")
		c.Git("commit", "-q", "--no-verify", "-m", "rel config")
		c.Git("tag", "v1.2")
		c.Git("push", "-q", "--no-verify", "origin", "refs/tags/v1.2")
		c.WriteFile("rel/env.yml", "KEY: "+key+"\nMORE: 1\n")
		c.Git("add", "rel/env.yml")
		c.TryGit("commit", "-q", "-m", "rel more").OK(t).
			Says(t, "rel/env.yml:1", "already on the remote in the same file (tag v1.2)").Never(t, key, rowWords)
	})

	w.Step("day1/after-a-squash-merge-names-the-remote-branch", func() {
		c.Git("checkout", "-q", "-b", "feat", "origin/main")
		c.WriteFile("feat/app.yml", "KEY: "+key+"\n")
		c.Git("add", "feat/app.yml")
		c.Git("commit", "-q", "--no-verify", "-m", "feat config")
		c.Git("checkout", "-q", "-B", "sq", "origin/main")
		c.Git("merge", "-q", "--squash", "feat")
		c.Git("commit", "-q", "--no-verify", "-m", "feat (squashed)")
		c.Git("push", "-q", "--no-verify", "origin", "HEAD:main")
		c.Git("fetch", "-q", "origin")
		c.Git("checkout", "-q", "feat")
		c.WriteFile("README.md", "# widgets\n")
		c.Git("add", "README.md")
		c.TryGit("commit", "-q", "-m", "readme").OK(t).
			Says(t, "feat/app.yml:1", "(origin/main)", "nothing new is sent").Never(t, key, rowWords)
	})

	w.Step("day1/a-new-key-still-blocks", func() {
		fresh := "AKIA" + strings.Repeat("J", 16)
		c.WriteFile("new.env", "K="+fresh+"\n")
		c.Git("add", "new.env")
		c.TryGit("commit", "-q", "-m", "new").Refused(t, "E_LEAK_BLOCKED", "new.env:1").Never(t, fresh)
	})
}
