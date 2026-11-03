// secret_scanner.go — the one constructor every write path uses to build
// its credential scanner.
package main

import (
	"path/filepath"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/security"
)

// piiPatternsFile is the per-user file of extra secret patterns, one regex
// per line (# comments), under ~/.lore.
const piiPatternsFile = "pii-patterns.txt"

// secretScanner returns the built-in credential patterns plus the user's
// own from ~/.lore/pii-patterns.txt.
//
// Why it exists: four write paths (memory add, rule add, sync export, task
// notes/handoffs) each built a bare security.NewScanner(), so a pattern the
// user added was never applied anywhere. Every path goes through here.
//
// Invariant: fails closed. An unreadable file or a pattern that does not
// compile is E_CONFIG_INVALID, never a silent fall back to the built-ins: a
// user who wrote a pattern expects it to block writes. A missing file is
// fine (no extra patterns). Patterns stored as `lore pii-pattern` rows are
// not loaded here yet (they need the project DB, opened after the scan).
func secretScanner() (*security.Scanner, error) {
	s := security.NewScanner()
	home, err := loreHome()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, piiPatternsFile)
	if err := s.LoadUserPatterns(path); err != nil {
		return nil, errcodes.New(errcodes.ConfigInvalid, "custom secret patterns: "+err.Error()).
			WithHint("fix or remove the bad line in " + path)
	}
	return s, nil
}
