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

// Phase 4 g1/g2 spec deltas (CLI_REFERENCE rule, decision, hotfix and
// pattern, playbook, prompt): on the core these nouns take the body
// either way, "body also positional" like memory. Before them this test
// pinned v0.1.17's refusal of positional text; it now pins that both
// spellings write the same body, and bodyOnlyArgs (TestBodyOnlyArgs) still
// guards the v0.1.17 commands that keep --body only.
func TestE2E_PositionalBodyIsRefused(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	alice.lore("init", "--non-interactive", "--name=app")
	for _, c := range []struct {
		args   []string
		prefix string
	}{
		{[]string{"rule", "add", "--severity=must", "never force-push main"}, "rul_"},
		{[]string{"rule", "add", "--body=use JWT"}, "rul_"},
		{[]string{"pattern", "add", "--title=t", "body text"}, "pat_"},
		{[]string{"memory", "add", "use JWT"}, "mem_"},
		{[]string{"memory", "add", "--body=use pgx"}, "mem_"},
	} {
		if r := alice.lore(c.args...); !strings.Contains(r.stdout, c.prefix) {
			t.Fatalf("lore %v: %s", c.args, r.stdout)
		}
	}
	for _, want := range []string{"never force-push main", "use JWT"} {
		if out := alice.lore("rule", "list").stdout; !strings.Contains(out, want) {
			t.Fatalf("rule list lacks %q: %s", want, out)
		}
	}
}
