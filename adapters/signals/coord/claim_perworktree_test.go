package coord

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/signals"
)

// TestPersonClaimsOnePerWorktree is devco:Q-1: a person's session is the
// same in every worktree, so the WIP limit counts the claims held in this
// checkout. Two worktrees each hold their own claim; a second claim in the
// same worktree is refused naming the config key and the flag to raise it.
func TestPersonClaimsOnePerWorktree(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	for _, id := range []string{"tsk_1", "tsk_2", "tsk_3"} {
		wd.addTask(id, nil)
	}
	p := wd.agent("pat")
	p.env.Caller.Label = "@pat"
	p.env.Agents.PerCheckout = true
	wt1 := p.env
	wt2 := p.env
	wt2.Held = HeldStore{Path: filepath.Join(t.TempDir(), "held.json")}

	if _, err := Claim(ctx, wt1, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatalf("first worktree claim: %v", err)
	}
	if _, err := Claim(ctx, wt2, "tsk_2", ClaimOptions{}); err != nil {
		t.Fatalf("a second worktree holds its own claim: %v", err)
	}
	_, err := Claim(ctx, wt1, "tsk_3", ClaimOptions{})
	var wip *signals.WIPError
	if !errors.As(err, &wip) {
		t.Fatalf("a second claim in one worktree is refused: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"this worktree already holds tsk_1", "agents.max_claims_per_agent 1", "lore config set --key agents.max_claims_per_agent --value 2", "another worktree"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, "tsk_2") || strings.Contains(msg, "session") {
		t.Errorf("refusal names another worktree's claim or a session: %q", msg)
	}
}

// TestAgentClaimLimitStaysPerSession: an agent session still holds one
// claim whatever checkout it claims from (the limit is per session).
func TestAgentClaimLimitStaysPerSession(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	wd.addTask("tsk_2", nil)
	a := wd.agent("bot")
	other := a.env
	other.Held = HeldStore{Path: filepath.Join(t.TempDir(), "held.json")}
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err := Claim(ctx, other, "tsk_2", ClaimOptions{})
	if !errors.Is(err, signals.ErrWIPLimit) || !strings.Contains(err.Error(), "this session already holds tsk_1") {
		t.Fatalf("an agent session holds one claim across checkouts: %v", err)
	}
}
