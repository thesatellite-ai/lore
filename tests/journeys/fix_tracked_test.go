//go:build journeys && !windows

package journeys

// fix_tracked_test.go — devco:D-744: `lore doctor --fix` in a repo an
// older lore set up changed tracked files (it moved lore's agent hooks out
// of the committed .claude/settings.json, moved the lore-sync-merge
// workflow's pin) and its output named none of them, so `git status`
// showed changes nobody was told to commit. Now every tracked file the run
// changes is named in a "commit these:" line (or a step's own commit line),
// the printed git command commits them and leaves nothing outside .lore/,
// and no line claims git status lists a file it does not.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	ftSettings = ".claude/settings.json"
	ftCommit   = "commit these: "
	// ftOldHooks is lore's Claude Code hooks in the team's settings file,
	// as a lore before D-732 wrote them and nothing else: the fix removes
	// the committed file (D-743), a tracked change it must name.
	ftOldHooks = `{
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "lore agent start --tool claude_code --hook-stdin"}]}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "lore agent end --tool claude_code", "timeout": 30}]}]
  }
}
`
)

func TestJourneyFixNamesTrackedFilesItChanges(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	app := w.Remote("github.com/orchard-dev/tracked")
	ida := w.Persona("Ida Marsh", "ida@orchard.example").Laptop()
	var c *teamharness.Clone

	w.Step("day1/an-older-lore-left-its-hooks-and-pin-in-tracked-files", func() {
		c = ida.Clone(app, "tracked")
		ida.LoreIn(c, "init", "--target", "claude_code").OK(t)
		ida.SetEnv(wfPinEnv, wfOld)
		ida.LoreIn(c, "sync", "install-action").OK(t)
		c.WriteFile(ftSettings, ftOldHooks)
		c.Git("rm", "-q", "--cached", "--ignore-unmatch", ".claude/settings.local.json")
		c.CommitAll("lore as an older version set it up")
	})

	w.Step("day2/doctor-fix-names-every-tracked-file-it-changed", func() {
		ida.SetEnv(wfPinEnv, wfNew)
		r := ida.LoreIn(c, "doctor", "--fix")
		out := r.Out()
		changed := trackedChanged(c)
		for _, want := range []string{ftSettings, wfFile} {
			if !strings.Contains(changed, want) {
				t.Fatalf("doctor --fix did not change %s (git status:\n%s)\n%s", want, changed, out)
			}
		}
		for _, p := range strings.Fields(changed) {
			if !namedForCommit(out, p) {
				t.Fatalf("doctor --fix changed the tracked %s and no commit line names it:\n%s", p, out)
			}
		}
		status := c.Git("status", "--porcelain", "--untracked-files=all")
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "which git status now lists") && strings.Contains(l, "settings.local.json") && !strings.Contains(status, "settings.local.json") {
				t.Fatalf("doctor --fix says git status lists a file it does not:\n%s\ngit status:\n%s", l, status)
			}
		}
		// the printed commit line, as printed, leaves nothing outside .lore/
		line := ""
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, ftCommit) {
				line = l
			}
		}
		add, ok := strings.CutPrefix(line[strings.Index(line, "git add -- "):], "git add -- ")
		if !ok {
			t.Fatalf("no git add in %q", line)
		}
		add, _, _ = strings.Cut(add, " && ")
		c.Git(append([]string{"add", "--"}, shellWords(add)...)...)
		for _, l := range strings.Split(out, "\n") {
			if _, cmd, found := strings.Cut(l, ": git add "); found && !strings.Contains(l, ftCommit) {
				files, _, _ := strings.Cut(cmd, " && ")
				c.Git(append([]string{"add"}, shellWords(files)...)...)
			}
		}
		c.Git("commit", "-qm", "lore: fix")
		if s := strayStatus(c); s != "" {
			t.Fatalf("after the printed commit lines, git status still lists:\n%s", s)
		}
	})

	w.Step("day2/a-second-fix-has-nothing-to-commit", func() {
		ida.LoreIn(c, "doctor", "--fix").Never(t, ftCommit)
	})
}

// trackedChanged is the tracked paths outside .lore/ git status lists as
// changed, one per line.
func trackedChanged(c *teamharness.Clone) string {
	var out []string
	for _, p := range strings.Split(c.Git("diff", "--name-only", "HEAD"), "\n") {
		if p != "" && !strings.HasPrefix(p, ".lore/") {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

// namedForCommit reports whether a line of out with a git add names path.
func namedForCommit(out, path string) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "git add") && strings.Contains(l, path) {
			return true
		}
	}
	return false
}

// shellWords splits s as a shell would for plain and single-quoted words.
func shellWords(s string) []string {
	var words []string
	var cur strings.Builder
	quoted, inWord := false, false
	for _, r := range s {
		switch {
		case r == '\'':
			quoted, inWord = !quoted, true
		case r == ' ' && !quoted:
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
			}
			inWord = false
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}
