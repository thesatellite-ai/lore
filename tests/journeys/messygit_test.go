//go:build journeys && !windows

package journeys

// messygit_test.go — two git shapes from a messy-git round:
//   - devco:Q-11: a team appends `* text=auto eol=crlf` after lore's
//     .gitattributes block. git applied the later line, so row files went
//     CRLF; lore "set up" its rules by adding a blank line, doctor said
//     nothing and render dirtied the pointer file. Now doctor names the
//     line, the next write moves lore's block to the end (row files LF
//     again) and render leaves the pointer file alone.
//   - devco:Q-3: a row committed on a detached HEAD: show's hint said a
//     force-push dropped it and "the author's clone may still have it";
//     now it names the detached HEAD and `git branch` to keep it.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// mgMemID finds a memory id in command output.
var mgMemID = regexp.MustCompile(`mem_[0-9a-f]+`)

func TestJourneyTeamCRLFAttributes(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	uma := w.Persona("Uma Urquhart", "uma@gizmos.example")
	remote := w.Remote("github.com/gizmos/crlfteam")
	var c *teamharness.Clone
	lore := func(args ...string) teamharness.Result { return uma.Laptop().LoreIn(c, args...) }

	w.Step("day1/lore-then-a-crlf-catch-all", func() {
		c = uma.Laptop().Clone(remote, "crlfteam")
		c.WriteFile("README.md", "crlfteam\n")
		c.CommitAll("init")
		c.Git("push", "-q", "origin", "HEAD:main")
		lore("init", "--non-interactive", "--name=crlfteam").OK(t)
		lore("memory", "add", "--body", "Seed note", "--no-live").OK(t)
		lore("render").OK(t)
		c.CommitAll("lore")
		attrs, _ := c.ReadFile(".gitattributes")
		c.WriteFile(".gitattributes", attrs+"\n# windows team\n* text=auto eol=crlf\n")
		c.Git("add", "--renormalize", ".")
		c.CommitAll("chore: crlf for the windows team")
		if err := os.Remove(filepath.Join(c.Dir, ".git", "index")); err != nil {
			t.Fatal(err)
		}
		c.Git("reset", "-q", "--hard")
		if eol := c.Git("check-attr", "eol", "--", ".lore/data/_meta.json"); !strings.HasSuffix(eol, "crlf") {
			t.Fatalf("setup: the team line does not apply: %s", eol)
		}
	})

	w.Step("day1/doctor-names-it-a-write-moves-the-block", func() {
		lore("doctor").Says(t, "* text=auto eol=crlf", "comes after lore's block", "lore fix")
		lore("memory", "add", "--body", "After the team rule", "--no-live").OK(t)
		if eol := c.Git("check-attr", "eol", "--", ".lore/data/_meta.json"); !strings.HasSuffix(eol, "lf") {
			t.Fatalf("after a write lore's eol=lf still does not apply: %s", eol)
		}
		lore("doctor").Never(t, "comes after lore's block")
		c.CommitAll("note and attributes")
		lore("render").OK(t)
		if st := c.Status(); st != "" {
			t.Fatalf("render dirtied tracked files under the team's crlf rule:\n%s", st)
		}
	})
}

func TestJourneyDetachedHeadRowHint(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	vic := w.Persona("Vic Vane", "vic@gizmos.example")
	remote := w.Remote("github.com/gizmos/detached")
	var c *teamharness.Clone
	lore := func(args ...string) teamharness.Result { return vic.Laptop().LoreIn(c, args...) }

	w.Step("day1/a-row-on-a-detached-head", func() {
		c = vic.Laptop().Clone(remote, "detached")
		c.WriteFile("README.md", "detached\n")
		c.CommitAll("init")
		c.Git("push", "-q", "origin", "HEAD:main")
		lore("init", "--non-interactive", "--name=detached").OK(t)
		c.CommitAll("lore init")
		c.Git("checkout", "-q", "--detach")
		id := mgMemID.FindString(lore("memory", "add", "--body", "Detached note", "--no-live").OK(t).Out())
		if id == "" {
			t.Fatal("memory add printed no id")
		}
		c.CommitAll("docs: detached note")
		sha := c.Git("rev-parse", "--short=7", "HEAD")
		c.Git("checkout", "-q", "main")
		lore("memory", "show", "--id", id).Refused(t, "E_NOT_FOUND", "made on a detached HEAD in this clone", "git branch detached-"+sha+" "+sha, "git cherry-pick "+sha).
			Never(t, "force-push", "author's clone")
	})
}
