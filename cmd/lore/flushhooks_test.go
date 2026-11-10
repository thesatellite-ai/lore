package main

import "testing"

// TestFlushHooks: the hooks lore already has push a waiting batch (store
// push modes; no daemon or timer exists), and nothing else does.
func TestFlushHooks(t *testing.T) {
	t.Parallel()
	yes := [][]string{
		{"agent", "start", "--tool", "claude-code", "--hook-stdin"},
		{"sync", "hook", "post-commit"},
		{"sync", "hook", "pre-push", "origin", "git@example.test:x.git"},
	}
	no := [][]string{
		{"agent", "start", "--tool", "claude-code"},
		{"sync"},
		{"sync", "hook", "pre-commit"},
		{"sync", "hook", "refresh"},
		{"memory", "add", "sync hook post-commit"},
	}
	for _, a := range yes {
		if !isSessionStartHook(a) && !isGitPushHook(a) {
			t.Errorf("%q does not flush", a)
		}
	}
	for _, a := range no {
		if isSessionStartHook(a) || isGitPushHook(a) {
			t.Errorf("%q flushes", a)
		}
	}
}
