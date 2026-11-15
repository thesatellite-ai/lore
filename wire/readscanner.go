package wire

import (
	"sync"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/logic/scan"
)

// readScanner is the scanner every read is masked with (devco:R-14,
// engine.WithReadScanner): lore's own rule pack (the well-known token
// shapes, private keys, passwords and urls with a password), built on the
// first scan. Not the full write rule set: that build reads the
// gitleaks defaults (~65 ms) and the known session ids through an open
// App, which a read inside the open cannot wait for; `lore check leaks
// --rows` still scans with every rule.
func readScanner() scan.Scanner { return packScanner{} }

// packScanner builds the pack scanner once per process on first use.
type packScanner struct{}

var packOnce = sync.OnceValue(func() scan.Scanner {
	s, err := gitleaks.New(gitleaks.Pack())
	if err != nil {
		return nil
	}
	return s
})

// Scan implements scan.Scanner. A pack that cannot be built (never with
// the fixed rules) masks nothing rather than every read.
func (packScanner) Scan(field, text string) []scan.Finding {
	s := packOnce()
	if s == nil {
		return nil
	}
	return s.Scan(field, text)
}
