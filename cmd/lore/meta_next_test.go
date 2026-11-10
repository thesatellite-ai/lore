package main

import (
	"testing"
)

// TestMetaHintsAreCommands (OV10 tests, later 433): every command a
// metadata error or warning names in its hint (engine/meta*.go) resolves
// against cmd/lore's real command tree with its flags, so a person or an
// agent who copies the hint runs a command that exists. Placeholders
// (<value>, ids) are written as the form a person would type.
func TestMetaHintsAreCommands(t *testing.T) {
	root := driftRoot(t)
	for _, code := range []string{
		"lore help meta",
		"lore meta keys --ids",
		"lore meta keys --unreviewed",
		"lore meta keys --unused",
		"lore meta keys --key client",
		"lore meta describe client",
		"lore meta edit client --merge-into customer",
		"lore meta edit client --no-internal",
		"lore meta forget client",
		"lore meta set mem_1 client=acme",
		"lore meta unset mem_1 client",
		"lore meta value add client acme",
		"lore meta lint",
		"lore store list",
		"lore sync",
		"lore move mem_1 --to me",
	} {
		if p := checkInvocation(root, code); p != "" {
			t.Errorf("hint %q: %s", code, p)
		}
	}
}
