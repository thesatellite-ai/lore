package gitleaks

import (
	"strings"
	"testing"
)

// TestGenericAPIKeyLoreShapes (item 30): gitleaks' generic-api-key rule
// is first confirmed to match sha1/sha256 hex digests and UUIDs (dashed
// and bare hex) next to a key word, as in a lore row file; the scanner
// then drops exactly those shapes, while real-looking generic keys on the
// same kind of line, including in the same text, are still flagged.
func TestGenericAPIKeyLoreShapes(t *testing.T) {
	t.Parallel()
	s := full(t)
	sha256Hex := "9f86d081884c7d659a2feaa0c55ad015" + "a3bf4f1b2b0b822cd15d6c15b0f00a08"
	sha1Hex := "2fd4e1c67a2d28fced84" + "9ee1bb76e7391b93eb12"
	uuidHex := "01a0ec295d707df2" + "81151dcef1a34bdb"
	uuidDash := "3b241101-e2bb-4255-" + "8caf-4136c566a962"
	lore := []string{
		`"token_hash": "` + sha256Hex + `"`,
		`"key_sha": "` + sha1Hex + `"`,
		`"key": "` + uuidHex + `"`,
		`"auth_uuid": "` + uuidDash + `"`,
	}
	for _, x := range lore {
		raw := 0
		for _, f := range s.detector.DetectString(x) {
			if f.RuleID == RuleGenericAPIKey {
				raw++
			}
		}
		if raw == 0 {
			t.Fatalf("fixture not confirmed: gitleaks' generic-api-key does not match %q", x)
		}
		if got := ruleIDs(s.Scan("", x)); got[RuleGenericAPIKey] {
			t.Errorf("lore value flagged by generic-api-key: %q", x)
		}
	}
	realKeys := []string{
		`"api_key": "` + "Zk3Qp9Lm2Xv8" + "Rt5Wn7Yb4Hc6Jd1Fg0Ks" + `"`,
		`"key": "` + "AB12CD34EF56AB12" + "CD34EF56AB12CD34" + `"`, // upper-case hex
		`"key": "` + "9f86d081884c0d65" + "9a2feaa0c55ad015" + `"`, // 32 hex, not a UUID (no version nibble)
		`"token": "` + "9f86d081884c7d65" + "9a2f" + `"`,           // 20 hex: no digest length
		`"key": "` + uuidHex + `", "api_key": "` + "Zk3Qp9Lm2Xv8" + "Rt5Wn7Yb4Hc6Jd1Fg0Ks" + `"`,
	}
	for _, x := range realKeys {
		if got := ruleIDs(s.Scan("", x)); !got[RuleGenericAPIKey] {
			t.Errorf("real-looking key not flagged: %d chars", len(x))
		}
	}
	// a named-format rule keeps running on digest-shaped text
	if aws := "AKIA" + strings.ToUpper("q7we9rt2yu4io6pa"); !ruleIDs(s.Scan("", "key "+aws))[RuleAWSAccessKey] {
		t.Error("aws key not flagged")
	}
}
