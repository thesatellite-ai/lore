//go:build journeys && !windows

package journeys

// session_worktrees_test.go — devco S-19. Rowan runs three agent sessions
// at once from one laptop: a Claude Code session in the main clone, a
// second Claude Code session in one worktree and a Codex session in
// another. lore session list, run in the main clone, counted only the
// writes made in that checkout: the two worktree sessions showed 0. Now
// each session's WRITES is its own count, wherever it wrote.

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneySessionListCountsWorktreeSessions(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lap := w.Persona("Rowan Pike", "rowan@vines.example").Laptop()
	var c *teamharness.Clone
	worktree := func(name string) *teamharness.Clone {
		dir := filepath.Join(filepath.Dir(c.Dir), name)
		c.Git("worktree", "add", "-q", "-b", name, dir)
		cp := *c
		cp.Dir = dir
		return &cp
	}

	w.Step("day1/setup", func() {
		c = lap.Clone(w.Remote("github.com/vines/trellis"), "trellis")
		lap.LoreIn(c, "init").OK(t)
		c.WriteFile("README.md", "# trellis\n")
		c.CommitAll("start")
		c.Git("push", "-q", "-u", "origin", "HEAD")
	})

	w.Step("day1/three-parallel-sessions", func() {
		wt1, wt2 := worktree("trellis-wt1"), worktree("trellis-wt2")
		writes := func(cl *teamharness.Clone, tool *jAgentsTool, session, place string, n int) {
			for i := range n {
				jAgentsRun(lap, cl, tool, session, "memory", "add", "--body", place+" note "+string(rune('a'+i)), "--yes").OK(t)
			}
		}
		writes(c, &jClaude, "cc-main-1", "main", 3)
		writes(wt2, &jClaude, "cc-wt2-2", "second", 2)
		writes(wt1, &jCodex, "cx-wt1-3", "first", 4)
		out := lap.LoreIn(c, "session", "list", "--json").OK(t).Stdout
		// The rows name no tool session (redacted outside a terminal), so
		// each is matched by its checkout folder (the repo column).
		var got struct {
			Sessions []struct {
				Tool   string `json:"tool"`
				Repo   string `json:"repo"`
				Writes int    `json:"writes"`
			} `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("session list --json: %v\n%s", err, out)
		}
		writesIn := map[string]int{}
		for _, r := range got.Sessions {
			writesIn[r.Tool+"@"+r.Repo] = r.Writes
		}
		// at least the memory writes of each (an agent's first write in a
		// repo also registers its actor copy)
		for key, least := range map[string]int{"claude_code@trellis": 3, "claude_code@trellis-wt2": 2, "codex@trellis-wt1": 4} {
			if writesIn[key] < least {
				t.Fatalf("session %s shows %d writes, want at least %d (each parallel session counts its own writes): %v", key, writesIn[key], least, writesIn)
			}
		}
	})
}
