package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"saas/pkg/aicoder/errcodes"
)

// TestSecretScannerLoadsUserPatterns pins that every write path's scanner
// applies ~/.lore/pii-patterns.txt: a user pattern used to be ignored.
func TestSecretScannerLoadsUserPatterns(t *testing.T) {
	const custom = "ACMEKEY-[0-9]{6}"
	for _, tc := range []struct {
		name    string
		file    *string // nil: no file at all
		body    string
		hit     bool
		errCode errcodes.Code
	}{
		{name: "no file: built-ins only", body: "ACMEKEY-123456", hit: false},
		{name: "custom pattern matches", file: strPtr("# team keys\n\n" + custom + "\n"), body: "key ACMEKEY-123456 here", hit: true},
		{name: "custom pattern, clean text", file: strPtr(custom + "\n"), body: "ACMEKEY-12", hit: false},
		{name: "built-ins still apply", file: strPtr(custom + "\n"), body: "AKIA" + "ABCDEFGHIJKLMNOP", hit: true},
		{name: "bad regex fails closed", file: strPtr("([unclosed\n"), errCode: errcodes.ConfigInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if tc.file != nil {
				dir := filepath.Join(home, ".lore")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, piiPatternsFile), []byte(*tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := secretScanner()
			if tc.errCode != "" {
				var cli *errcodes.CLIError
				if !errors.As(err, &cli) || cli.Code != tc.errCode {
					t.Fatalf("secretScanner() err = %v, want %s", err, tc.errCode)
				}
				if check := secretCheck(); check == nil || check([]byte("anything")) == "" {
					t.Fatal("secretCheck must refuse every file when the patterns are broken")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := len(s.Scan(tc.body)) > 0; got != tc.hit {
				t.Fatalf("Scan(%q) hit = %v, want %v", tc.body, got, tc.hit)
			}
			if tc.hit {
				if err := refuseSecrets(tc.body); err == nil {
					t.Fatal("refuseSecrets must refuse what the scanner matches")
				}
			}
		})
	}
}

func strPtr(s string) *string { return &s }
