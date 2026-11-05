package main

import (
	"strings"
	"testing"

	"saas/pkg/aicoder/errcodes"
)

// TestRefuseSecretsUsesTheCoreRules: with the core mounted, the task
// note/lesson pre-check scans with the core's write-time rules, so a custom
// secret_rules row (which the v0.1.17 scanner never knew) refuses, naming
// the rule id and never the value.
func TestRefuseSecretsUsesTheCoreRules(t *testing.T) {
	f := newCoreFixture(t)
	f.lore("secrets", "rule", "add", "acme-token", "--regex", `ACMETOK-[0-9]{12}`, "--yes")
	f.mount()
	token := "ACMETOK-" + strings.Repeat("7", 12)
	err := refuseSecrets("deploy with " + token)
	if !errcodes.IsCode(err, errcodes.SecretDetected) || !strings.Contains(err.Error(), "acme-token") || strings.Contains(err.Error(), token) {
		t.Fatalf("refuseSecrets = %v, want E_SECRET_DETECTED naming acme-token without the value", err)
	}
	if err := refuseSecrets("plain text"); err != nil {
		t.Fatalf("plain text: %v", err)
	}
}
