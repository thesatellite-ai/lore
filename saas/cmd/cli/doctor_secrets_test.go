package main

import (
	"context"
	"strings"
	"testing"
)

// TestDoctorSecretRulesEnv (D3): a leftover LORE_SYNC_ALLOW_SECRETS is a
// warning that degrades health and names the replacement; unset, nothing.
func TestDoctorSecretRulesEnv(t *testing.T) {
	saved := coreApp
	coreApp = nil
	t.Cleanup(func() { coreApp = saved })
	r := &doctorReport{Status: doctorHealthy}
	doctorSecretRules(context.Background(), r, true)
	if len(r.Warnings) != 0 {
		t.Fatalf("unset env warned: %v", r.Warnings)
	}
	t.Setenv(envRemovedSyncAllow, "1")
	doctorSecretRules(context.Background(), r, true)
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "lore secrets rule off") || r.Status == doctorHealthy {
		t.Fatalf("warnings %v status %v", r.Warnings, r.Status)
	}
}
