package main

// sessionend_test.go — the session-end hook never blocks or fails the
// tool's session: exit 0, nothing on stdout or stderr, whatever the
// folder, the flags or the machine (decided 2026-11-10, land7 A).

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestIsSessionEndHook: only `agent end` (after any global flags) is the hook.
func TestIsSessionEndHook(t *testing.T) {
	t.Parallel()
	for _, a := range [][]string{
		{"agent", "end", "--tool", "claude_code"},
		{"--trace", "agent", "end"},
		{"agent", "end", "--tool", "codex", "--bogus"},
	} {
		if !isSessionEndHook(a) {
			t.Errorf("%q is not the end hook", a)
		}
	}
	for _, a := range [][]string{
		{"agent", "start", "--tool", "claude_code", "--hook-stdin"},
		{"memory", "add", "agent end"},
		{"agent"},
		{"end", "agent"},
	} {
		if isSessionEndHook(a) {
			t.Errorf("%q is taken for the end hook", a)
		}
	}
}

// TestSessionEndHookQuietZero runs the hook where every other lore command
// would fail or print: outside any repo, in a git repo without lore, with
// a flag this lore does not know, with a relative HOME (refused before any
// command runs), and with tool JSON on stdin. Each is exit 0 with empty
// stdout and stderr, well inside the budget. Break-verified by removing
// the isSessionEndHook branch of main (the relative HOME and the unknown
// flag exit non-zero with an error line).
func TestSessionEndHookQuietZero(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t)
	plain := w.path("plain")
	if err := os.MkdirAll(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	gitRepo := w.path("gitrepo")
	if err := os.MkdirAll(gitRepo, 0o700); err != nil {
		t.Fatal(err)
	}
	w.git(t, gitRepo, "init", "-q")
	stdin := `{"session_id":"s-placeholder","hook_event_name":"SessionEnd","reason":"exit"}`
	cases := []struct {
		name  string
		dir   string
		extra map[string]string
		args  []string
	}{
		{"outside a repo", plain, nil, []string{"agent", "end", "--tool", "claude_code"}},
		{"git repo without lore", gitRepo, nil, []string{"agent", "end", "--tool", "codex"}},
		{"unknown flag", gitRepo, nil, []string{"agent", "end", "--tool", "gemini_cli", "--from-a-newer-lore"}},
		{"relative HOME", plain, map[string]string{"HOME": filepath.Join("relative", "home")}, []string{"agent", "end", "--tool", "claude_code"}},
	}
	for _, c := range cases {
		start := time.Now()
		code, out, errOut := w.loreRun(t, c.dir, c.extra, stdin, time.Minute, c.args...)
		if code != 0 || out != "" || errOut != "" {
			t.Errorf("%s: want exit 0 and no output, got %d stdout %q stderr %q", c.name, code, out, errOut)
		}
		if d := time.Since(start); d > 30*time.Second {
			t.Errorf("%s: the hook took %s", c.name, d)
		}
	}
}
