package app

import (
	"strings"
	"testing"
)

// TestRunnableNounHelpListsJSON: a noun that runs on its own and also has
// subcommands (`lore sync`, `lore trash`, `lore doctor`, …) lists --json
// in its own --help, as every leaf does; it was once lost there (found by
// gate run 2). A pure group (`lore memory`) keeps --json hidden: it only
// refuses an unknown word.
func TestRunnableNounHelpListsJSON(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t)
	dir := w.path(".")
	for _, noun := range []string{"sync", "trash", "doctor", "whoami", "search", "lint", "land", "upgrade"} {
		code, out, errOut := w.lore(t, dir, noun, "--help")
		if code != 0 || !strings.Contains(out, "Available Commands:") {
			t.Errorf("lore %s --help: exit %d, want a group's help:\n%s%s", noun, code, out, errOut)
			continue
		}
		if !strings.Contains(flagsBlock(out), "--json") {
			t.Errorf("lore %s --help lists no --json in its Flags:\n%s", noun, out)
		}
	}
	if _, out, _ := w.lore(t, dir, "memory", "--help"); strings.Contains(flagsBlock(out), "--json") {
		t.Errorf("lore memory --help lists the group's hidden --json:\n%s", out)
	}
}

// flagsBlock is the local "Flags:" block of a help text (up to the next
// blank line), without the Global Flags.
func flagsBlock(help string) string {
	_, rest, ok := strings.Cut(help, "\nFlags:\n")
	if !ok {
		return ""
	}
	block, _, _ := strings.Cut(rest, "\n\n")
	return block
}
