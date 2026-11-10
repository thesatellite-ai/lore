package identity

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestKeyIsAnEmail is v0.1.17's TestFix_FallbackIdentityKeyIsAnEmail
// (saas/cmd/cli/cmd_fixes_test.go), moved with the code (PASS3 ONE-PATH):
// the identity key is always human:<email>; a bare name, a malformed
// --key or two emails that disagree are E_INVALID_INPUT.
func TestKeyIsAnEmail(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		arg, key           string
		wantDisplay, wantK string
		wantErr            bool
	}{
		{arg: "alice@example.com", wantDisplay: "alice@example.com", wantK: "human:alice@example.com"},
		{arg: "Alice Doe <alice@example.com>", wantDisplay: "Alice Doe", wantK: "human:alice@example.com"},
		{arg: "Alice Doe", key: "human:alice@example.com", wantDisplay: "Alice Doe", wantK: "human:alice@example.com"},
		{arg: "alice@example.com", key: "human:alice@example.com", wantDisplay: "alice@example.com", wantK: "human:alice@example.com"},
		{arg: "alice", wantErr: true},
		{arg: "Alice Doe", wantErr: true},
		{arg: "   ", wantErr: true},
		{arg: "Alice", key: "alice@example.com", wantErr: true},
		{arg: "Alice", key: "human:alice", wantErr: true},
		{arg: "Alice", key: "human:Alice <alice@example.com>", wantErr: true},
		{arg: "bob@example.com", key: "human:alice@example.com", wantErr: true},
	} {
		display, key, err := Key(tc.arg, tc.key)
		if tc.wantErr {
			if !errs.Has(err, errs.InvalidInput) {
				t.Fatalf("Key(%q,%q) = %q,%q,%v; want E_INVALID_INPUT", tc.arg, tc.key, display, key, err)
			}
			continue
		}
		if err != nil || display != tc.wantDisplay || key != tc.wantK {
			t.Fatalf("Key(%q,%q) = %q,%q,%v", tc.arg, tc.key, display, key, err)
		}
	}
}
