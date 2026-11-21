//go:build journeys && !windows

package journeys

// ask_file_test.go — devco:D-815 (bug hunt H-74, the coding-agent context
// gate): an agent about to change billing/invoice.go asked `lore ask` and
// got "nothing in the store answers it", while the repo's notes held a
// must-rule for billing/** and a critical hotfix about that very file.
// Now a question naming a file answers with what lore context --for-file
// picks for it, and a note sharing one word with the question stays out.

import (
	"testing"
)

func TestJourneyAskNamesAFile(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ravi := w.Persona("Ravi Menon", "ravi@acme-co.example")
	lap := ravi.Laptop()
	code := lap.Clone(w.Remote("github.com/acme-co/orbit"), "orbit")

	w.Step("day1/rules-and-a-hotfix-for-billing", func() {
		lap.LoreIn(code, "init").OK(t)
		lap.LoreIn(code, "rule", "add", "--body", "Money amounts are int64 cents in billing; never float64.", "--activation", "glob", "--globs", `["billing/**"]`, "--yes").OK(t)
		lap.LoreIn(code, "hotfix", "add", "--title", "Invoices dated a day off", "--body", "billing/invoice.go dated invoices in local time: use clock.UTC() for the issue date, never time.Now().", "--severity", "critical", "--yes").OK(t)
		lap.LoreIn(code, "memory", "add", "--body", "Customers asked for a bigger font on the invoice header.", "--yes").OK(t)
	})

	w.Step("day1/the-agent-asks-before-it-edits", func() {
		lap.LoreIn(code, "ask", "--query", "set the issue date when an invoice is finalized in billing/invoice.go").OK(t).
			Says(t, "applies to billing/invoice.go", "never float64", "Invoices dated a day off", "clock.UTC()", "cited: rul_").
			Never(t, "bigger font", "nothing in the store answers it")
		// without a file the hotfix is found by its words
		lap.LoreIn(code, "ask", "--query", "invoice issue date trap").OK(t).
			Says(t, "Invoices dated a day off")
	})
}
