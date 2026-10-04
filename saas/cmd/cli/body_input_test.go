package main

import (
	"strings"
	"testing"
)

// Positional text on a body-primary `add` command is refused before RunE,
// with the message that points at --body (not cobra's "unknown command").
func TestBodyOnlyArgs(t *testing.T) {
	t.Parallel()
	if err := bodyOnlyArgs(nil, nil); err != nil {
		t.Fatalf("no positional args must pass: %v", err)
	}
	err := bodyOnlyArgs(nil, []string{"some text"})
	if err == nil || !strings.Contains(err.Error(), "--body") {
		t.Fatalf("positional text must point at --body: %v", err)
	}
}

func TestE2E_PositionalBodyIsRefused(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	alice.lore("init", "--non-interactive", "--name=app")
	for _, args := range [][]string{
		{"memory", "add", "use JWT"},
		{"rule", "add", "--severity=must", "never force-push main"},
		{"pattern", "add", "--title=t", "body text"},
	} {
		r := alice.loreAny(args...)
		if r.code == 0 || !strings.Contains(r.stderr, "E_INVALID_INPUT") || !strings.Contains(r.stderr, "--body") {
			t.Fatalf("lore %v: exit %d\n%s", args, r.code, r.stderr)
		}
	}
	if r := alice.lore("memory", "add", "--body=use JWT"); !strings.Contains(r.stdout, "mem_") {
		t.Fatalf("--body must work: %s", r.stdout)
	}
}
