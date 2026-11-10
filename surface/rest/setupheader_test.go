package rest

import (
	"testing"

	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// TestSetupHeaderName: the REST header spelled here is the contract name
// (ONBOARDING_DESIGN 7.1).
func TestSetupHeaderName(t *testing.T) {
	if headerSetup != opssetup.HeaderSetup {
		t.Fatalf("headerSetup %q != opssetup.HeaderSetup %q", headerSetup, opssetup.HeaderSetup)
	}
}
