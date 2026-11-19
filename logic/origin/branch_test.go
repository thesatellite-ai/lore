package origin_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/origin"
)

// TestBranch: `_o.git.branch` is read from a built block; a detached
// HEAD, no git block and no block at all read as no branch (D-392).
func TestBranch(t *testing.T) {
	build := func(g origin.Git) string {
		o, err := origin.Build(origin.Facts{Cmd: "memory add", Git: g})
		if err != nil {
			t.Fatal(err)
		}
		return origin.Branch(o)
	}
	if got := build(origin.Git{State: origin.GitBranch, Branch: "feat/x", Commit: "abc123"}); got != "feat/x" {
		t.Fatalf("Branch = %q, want feat/x", got)
	}
	if got := build(origin.Git{State: origin.GitDetached, Commit: "abc123"}); got != "" {
		t.Fatalf("detached Branch = %q, want empty", got)
	}
	if got := build(origin.Git{}); got != "" {
		t.Fatalf("no git Branch = %q, want empty", got)
	}
}
