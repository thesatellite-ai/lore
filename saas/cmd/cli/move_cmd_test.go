package main

import (
	"strings"
	"testing"
)

// TestShareIsAnUnknownCommand (BATCH 2: `lore move` is the one command;
// `lore share` is removed): `lore share …` answers exactly like any other
// unknown root command (same exit, same error line with the name swapped; cobra may add its usual
// "Did you mean" suggestion), so
// there is no alias, no hidden stub and no hint special-cased for it.
func TestShareIsAnUnknownCommand(t *testing.T) {
	f := newCoreFixture(t)
	const id = "mem_0123456789abcdef0123456789abcdef"
	share := f.run(e2eBin, "share", id, "--to", "repo", "--yes")
	other := f.run(e2eBin, "nosuchverb", id, "--to", "repo", "--yes")
	if share.code != flag2Exit || !strings.Contains(share.stderr, `unknown command "share" for "lore"`) {
		t.Fatalf("lore share: exit %d, want %d with cobra's unknown-command error:\n%s", share.code, flag2Exit, share.stderr)
	}
	// cobra's own "Did you mean" suggestions differ by name; the error line
	// itself must be the generic one.
	first := func(s string) string { l, _, _ := strings.Cut(s, "\n"); return l }
	if share.code != other.code || strings.ReplaceAll(first(share.stderr), `"share"`, `"nosuchverb"`) != first(other.stderr) {
		t.Fatalf("lore share must answer like any unknown command:\nshare (exit %d):\n%s\nnosuchverb (exit %d):\n%s", share.code, share.stderr, other.code, other.stderr)
	}
	if strings.Contains(share.stderr, "lore move") {
		t.Fatalf("no hint special-cased for share: %s", share.stderr)
	}
}
