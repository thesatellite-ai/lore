package main

import (
	"errors"
	"strings"
	"testing"

	"saas/pkg/aicoder/errcodes"
)

// TestValidPluginSHA256 pins the --sha256 check of `lore plugin trust`: a
// short or non-hex value used to be saved and then crash `plugin list`.
func TestValidPluginSHA256(t *testing.T) {
	t.Parallel()
	full := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name, in string
		ok       bool
	}{
		{"full lowercase", full, true},
		{"full uppercase", strings.ToUpper(full), true},
		{"empty", "", false},
		{"short hex", "abc", false},
		{"odd length", full[:63], false},
		{"one too long", full + "a", false},
		{"two too long", full + "ab", false},
		{"non-hex", strings.Repeat("zz", 32), false},
		{"sha256 prefix", "sha256:" + full[:57], false},
		{"unicode", strings.Repeat("é", 32), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validPluginSHA256(tc.in)
			if tc.ok {
				if err != nil {
					t.Fatalf("validPluginSHA256(%q) = %v, want nil", tc.in, err)
				}
				return
			}
			var cli *errcodes.CLIError
			if !errors.As(err, &cli) || cli.Code != errcodes.InvalidInput {
				t.Fatalf("validPluginSHA256(%q) = %v, want E_INVALID_INPUT", tc.in, err)
			}
		})
	}
}

// TestShortFingerprint pins that listing never slices past a stored value
// (rows saved before validation may be short).
func TestShortFingerprint(t *testing.T) {
	t.Parallel()
	full := strings.Repeat("0123456789", 7)
	for in, want := range map[string]string{
		"":        "",
		"abc":     "abc",
		full[:12]: full[:12],
		full[:13]: full[:12],
		full:      full[:12],
	} {
		if got := shortFingerprint(in); got != want {
			t.Errorf("shortFingerprint(%q) = %q, want %q", in, got, want)
		}
	}
}
