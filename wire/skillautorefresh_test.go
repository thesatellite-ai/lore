package wire

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/ops/skill"
)

// TestSkillAutoRefreshOncePerDay (H-185, devco:D-927): every command calls
// the skill check, but the refresh itself runs once a day per binary
// version: the hot path is one stat and one small read. A new binary
// (brew upgrade) checks at once, a day later it checks again, and a home
// that cannot hold the stamp never runs it (it would run on every command).
func TestSkillAutoRefreshOncePerDay(t *testing.T) {
	ctx := context.Background()
	state := filepath.Join(t.TempDir(), skillCheckDir, skillCheckFile)
	calls := 0
	refresh := func(context.Context) (skill.RefreshOut, error) {
		calls++
		return skill.RefreshOut{BinaryVersion: "0.1.40", Refreshed: []skill.Refreshed{{Tool: "codex", To: "0.1.40"}}}, nil
	}
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	var w bytes.Buffer
	for i := range 5 {
		autoRefreshSkill(ctx, state, "0.1.40", now.Add(time.Duration(i)*time.Minute), refresh, &w)
	}
	if calls != 1 {
		t.Fatalf("5 commands in one day ran the refresh %d times, want 1", calls)
	}
	if got := w.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "refreshed the lore skill for codex to lore 0.1.40") {
		t.Fatalf("stderr: %q", got)
	}
	autoRefreshSkill(ctx, state, "0.1.41", now.Add(time.Hour), refresh, &w)
	if calls != 2 {
		t.Fatalf("a new binary version did not check at once: %d calls", calls)
	}
	autoRefreshSkill(ctx, state, "0.1.41", now.Add(2*time.Hour), refresh, &w)
	autoRefreshSkill(ctx, state, "0.1.41", now.Add(25*time.Hour), refresh, &w)
	if calls != 3 {
		t.Fatalf("a day later: %d calls, want 3", calls)
	}

	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before := calls
	for range 3 {
		autoRefreshSkill(ctx, filepath.Join(blocked, skillCheckDir, skillCheckFile), "0.1.41", now, refresh, &w)
	}
	if calls != before {
		t.Fatalf("a home that cannot hold the stamp ran the refresh %d times", calls-before)
	}
}

// TestSkillRefreshLines: a left skill names its fix on one line.
func TestSkillRefreshLines(t *testing.T) {
	lines := skillRefreshLines(skill.RefreshOut{BinaryVersion: "0.1.40", Left: []skill.StaleLeft{{Tool: "codex", Detail: "behind", Fix: "git -C /co pull"}}})
	if len(lines) != 1 || lines[0] != "lore: the codex skill is stale: behind; run git -C /co pull" {
		t.Fatalf("lines: %q", lines)
	}
}
