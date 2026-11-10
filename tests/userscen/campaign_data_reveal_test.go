//go:build userscen

package userscen

// campaign_data_reveal_test.go — `secrets show --reveal` and `show
// --expand` against the fake vault only (cdFakeVault): without a person's
// yes nothing is read, with --yes exactly the one entry is read and
// printed, --expand alone never opens the vault, and an entry the vault
// lacks is named without a value.

import (
	"os"
	"strings"
	"testing"
)

// TestCampaignData_Reveal: ask first, read one entry, print it once.
func TestCampaignData_Reveal(t *testing.T) {
	c := resRepo(t)
	m := cdIDOf(t, c.Lore("memory", "add", "--body", "db ${secret:gopass://acme/db/password} and ${secret:gopass://acme/db/missing}", "--json"))
	fv, log := cdFakeVault(t, c)
	calls := func() string { b, _ := os.ReadFile(log); return string(b) }

	if r := fv.Lore("show", m, "--expand"); r.Code != 0 || strings.Contains(r.Out(), "placeholder-value") {
		t.Fatalf("--expand printed a vault value: %s", r)
	}
	if calls() != "" {
		t.Fatalf("--expand opened the vault: %q", calls())
	}
	cdRefused(t, fv.Lore("secrets", "show", "gopass://acme/db/password", "--reveal", "--json"), "E_NEEDS_CONFIRM")
	if calls() != "" {
		t.Fatalf("an unconfirmed reveal opened the vault: %q", calls())
	}
	r := fv.Lore("secrets", "show", "gopass://acme/db/password", "--reveal", "--yes")
	if r.Code != 0 || strings.Count(r.Out(), "placeholder-value") != 1 {
		t.Fatalf("a confirmed reveal: %s", r)
	}
	for _, l := range strings.Split(strings.TrimSpace(calls()), "\n") {
		if l != "show -o acme/db/password" {
			t.Errorf("reveal of one entry ran gopass %q", l)
		}
	}
	miss := fv.Lore("secrets", "show", "gopass://acme/db/missing", "--reveal", "--yes", "--json")
	if miss.Code == 0 && !strings.Contains(miss.Out(), "missing") {
		t.Errorf("a missing entry: %s", miss)
	}
	if strings.Contains(miss.Out(), "placeholder-value") {
		t.Errorf("a missing entry printed another entry's value: %s", miss)
	}
}
