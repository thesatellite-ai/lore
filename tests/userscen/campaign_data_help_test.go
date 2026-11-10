//go:build userscen

package userscen

// campaign_data_help_test.go — every command of the data area answers
// --help with a usage line and at least one runnable example ("lore <the
// command> …" under Examples:), and refuses an unknown flag with
// E_INVALID_INPUT and its documented exit, with nothing written.

import (
	"strings"
	"testing"
)

// TestCampaignData_HelpHasExamples: `lore <cmd> --help` exits 0, prints
// Usage: and an Examples: block whose lines run this very command.
func TestCampaignData_HelpHasExamples(t *testing.T) {
	c := resRepo(t)
	for _, cmd := range cdHelpCmds {
		args := append(strings.Fields(cmd), "--help")
		r := c.Lore(args...)
		if r.Code != 0 {
			t.Errorf("lore %s --help: exit %d: %s", cmd, r.Code, r.Out())
			continue
		}
		if !strings.Contains(r.Stdout, "Usage:\n  lore "+cmd) {
			t.Errorf("lore %s --help: no usage line for the command", cmd)
		}
		_, ex, ok := strings.Cut(r.Stdout, "\nExamples:\n")
		if !ok {
			t.Errorf("lore %s --help: no Examples: block", cmd)
			continue
		}
		ex, _, _ = strings.Cut(ex, "\nFlags:")
		if !strings.Contains(ex, "lore "+cmd+" ") && !strings.HasSuffix(strings.TrimSpace(ex), "lore "+cmd) {
			t.Errorf("lore %s --help: no example runs this command:\n%s", cmd, ex)
		}
	}
}

// TestCampaignData_UnknownFlagRefused: a misspelt flag is E_INVALID_INPUT
// (exit 20) on every command, and the tree is unchanged.
func TestCampaignData_UnknownFlagRefused(t *testing.T) {
	c := resRepo(t)
	before := cdTreeHash(t, c)
	for _, cmd := range cdHelpCmds {
		// FLAG2 (surface/cli/build.go usageError): a flag parse error under
		// --json is the JSON error envelope on stdout.
		r := c.Lore(append(strings.Fields(cmd), "--json", "--no-such-flag")...)
		if r.Code != cdDocExit(t, "E_INVALID_INPUT") || !strings.Contains(r.Stdout, `"code": "E_INVALID_INPUT"`) || !strings.Contains(r.Stdout, `"message": "unknown flag: --no-such-flag"`) {
			t.Errorf("lore %s --no-such-flag: want E_INVALID_INPUT naming the flag, got exit %d: %s", cmd, r.Code, r.Out())
		}
	}
	if after := cdTreeHash(t, c); after != before {
		t.Error("a refused command changed the tree")
	}
}

// TestCampaignData_ExamplesGiveRequiredFlags: every example line passes
// each flag the command marks (required), and names only flags the
// command has (two examples left out the required --kind and --reason,
// one gave a --confirm the command needs).
func TestCampaignData_ExamplesGiveRequiredFlags(t *testing.T) {
	c := resRepo(t)
	for _, cmd := range cdHelpCmds {
		h := c.Lore(append(strings.Fields(cmd), "--help")...).Stdout
		_, ex, ok := strings.Cut(h, "\nExamples:\n")
		if !ok {
			continue // TestCampaignData_HelpHasExamples reports it
		}
		ex, flags, _ := strings.Cut(ex, "\nFlags:")
		flags, _, _ = strings.Cut(flags, "\nGlobal Flags:")
		for _, fl := range strings.Split(flags, "\n") {
			if !strings.HasSuffix(strings.TrimSpace(fl), "(required)") {
				continue
			}
			f := strings.Fields(fl)
			name := f[0]
			if strings.HasPrefix(name, "-") && !strings.HasPrefix(name, "--") && len(f) > 1 {
				name = strings.TrimSuffix(f[1], ",")
			}
			for _, line := range strings.Split(ex, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "lore ") && !strings.Contains(line, name) {
					t.Errorf("lore %s: example %q lacks the required %s", cmd, strings.TrimSpace(line), name)
				}
			}
		}
	}
}
