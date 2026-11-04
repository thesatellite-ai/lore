package main

import (
	"strings"
	"testing"

	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
)

// TestAskAddressOnlyFirstRunInTerminal pins Q2: lore asks only on the run
// that first records an address and only in a terminal; without one (an
// agent, CI) it never prompts, and a repeat notice never prompts either.
func TestAskAddressOnlyFirstRunInTerminal(t *testing.T) {
	cases := []struct {
		isNew, terminal, want bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	}
	for _, c := range cases {
		if got := askAddress(opsinfra.AddressNotice{New: c.isNew}, c.terminal); got != c.want {
			t.Errorf("new=%v terminal=%v: ask=%v, want %v", c.isNew, c.terminal, got, c.want)
		}
	}
}

// TestAddressNoticeCaseOnlyOffersNoFork pins PROV3 in the notice text: a
// case-only difference names set-url and never `lore repo fork`; a real
// change offers all three commands.
func TestAddressNoticeCaseOnlyOffersNoFork(t *testing.T) {
	c := addressNotice("rep_x", opsinfra.AddressNotice{Key: "github.com/Agency/x", Recorded: "github.com/agency/x", CaseOnly: true})
	if strings.Contains(c, "fork") || !strings.Contains(c, "lore repo set-url github.com/Agency/x") {
		t.Fatalf("case-only notice: %s", c)
	}
	n := addressNotice("rep_x", opsinfra.AddressNotice{Key: "github.com/tomas/x", Recorded: "github.com/agency/x"})
	for _, want := range []string{"lore repo set-url github.com/tomas/x ", "--mirror", "lore repo fork", "rep_x"} {
		if !strings.Contains(n, want) {
			t.Fatalf("notice lacks %q: %s", want, n)
		}
	}
}
