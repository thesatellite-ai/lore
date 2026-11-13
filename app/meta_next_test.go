package app

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
		"lore meta describe --id client --text \"…\"",
		"lore meta edit --id client --merge-into customer",
		"lore meta edit --id client --no-internal",
		"lore meta forget --key client",
		"lore meta set --id mem_1 --pair client=acme",
		"lore meta unset --id mem_1 --key client",
		"lore meta value add --key client --value acme",
		"lore meta lint",
		"lore store list",
		"lore sync",
		"lore move --id mem_1 --to me",
	} {
		if p := checkInvocation(root, code); p != "" {
			t.Errorf("hint %q: %s", code, p)
		}
	}
}
