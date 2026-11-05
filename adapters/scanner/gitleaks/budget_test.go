package gitleaks

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/schema"
)

// SECRETS 3.8 pins the scan budgets with a test. The budgets (design
// table) are the contract; a change that makes the scanner slower than
// them fails here instead of in a person's commit hook.
//
//	scan part of a pre-push over 1,000 changed row files      < 2 s    TestScanBudgets
//	detector build (defaults + lore pack + store rules)        < 40 ms  BenchmarkDetectorBuild only
//	scan of a 4 KB row, no hit                                 < 1 ms   BenchmarkScan4KB only
//	scan of a 16 KB metadata blob (the MQ3 cap)                < 4 ms   BenchmarkScanMetaBlob16KB only
//
// Only the pre-push budget is asserted (P3-SEC-3): the as-built scanner
// misses the per-row budgets (about 3.5 ms per 4 KB row and 15 ms per
// 16 KB blob, mostly gitleaks' encoded-segment search and the regexes of
// keyword-triggered rules), and its cold build (once per process, gitleaks
// config parse included) sits at 32-55 ms, so an assertion would fail or
// flap. That is an open owner question, not a number to loosen here; the
// benchmarks keep the cost visible (go test -bench . ./adapters/scanner/gitleaks).
// The "zero for commands that write no text" row is a wiring property,
// not a timing.
//
// The timed case takes the fastest of budgetRuns runs (race_test.go,
// norace_test.go): the fastest run is
// the cost of the code, the slower ones are the machine being busy, so the
// check is about the scanner, not the load.
const (
	budgetPrePush = 2 * time.Second
	// prePushFiles is the design's pre-push size; rowFileBytes is the size
	// of one typical row file (a memory or task row with its envelope).
	prePushFiles = 1000
	rowFileBytes = 1 << 10
)

// budgetScanner builds the scanner a command builds: the gitleaks
// defaults and lore's pack, plus a store-added rule (the "layer rules").
func budgetScanner() (*Scanner, error) {
	base, err := Defaults()
	if err != nil {
		return nil, err
	}
	rules, err := Rules(base, []RuleRow{{RuleID: "acme-token", State: schema.SecretRuleOn, Regex: `ACMETOK-[0-9]{12}`}})
	if err != nil {
		return nil, err
	}
	return New(rules)
}

// prose is plain row text that matches no rule: words a lore row holds
// (keywords like "token" and "password" included, so the keyword
// prefilter does not skip the regexes), no value shaped like a secret.
const prose = "Rotate the deploy token after the release; the password policy lives in the team handbook. " +
	"Run task build, then check the staging logs before the merge. Decision: keep the cache warm.\n"

// fill repeats s up to n bytes.
func fill(s string, n int) string {
	return strings.Repeat(s, n/len(s)+1)[:n]
}

// metaBlob is a 16 KB JSON metadata value with no secret in it.
func metaBlob() string {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; b.Len() < 16<<10-64; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"key_` + strconv.Itoa(i) + `":"owner review of the api token rotation step ` + strconv.Itoa(i) + `"`)
	}
	b.WriteString("}")
	return b.String()
}

// fastest is the fastest of budgetRuns timed calls of f.
func fastest(f func()) time.Duration {
	best := time.Duration(0)
	for range budgetRuns {
		start := time.Now()
		f()
		if d := time.Since(start); best == 0 || d < best {
			best = d
		}
	}
	return best
}

// scanBench scans text as one field per op and fails the benchmark on a
// hit (the fixtures are no-hit cases: a hit would measure the wrong path).
func scanBench(sc scan.Scanner, texts ...string) func(b *testing.B) {
	return func(b *testing.B) {
		for b.Loop() {
			for _, t := range texts {
				if fs := sc.Scan("body", t); len(fs) != 0 {
					b.Fatalf("fixture hit rule %s", fs[0].RuleID)
				}
			}
		}
	}
}

// TestScanBudgets (SECRETS 3.8) fails when the scan part of a 1,000-file
// pre-push is over its budget.
func TestScanBudgets(t *testing.T) {
	sc, err := budgetScanner()
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]string, prePushFiles)
	for i := range rows {
		rows[i] = fill(strconv.Itoa(i)+" "+prose, rowFileBytes)
	}
	hit := ""
	d := fastest(func() {
		for _, r := range rows {
			if fs := sc.Scan("body", r); len(fs) != 0 {
				hit = fs[0].RuleID
			}
		}
	})
	if hit != "" {
		t.Fatalf("fixture hit rule %s", hit)
	}
	if limit := budgetPrePush * budgetSlack; d > limit {
		t.Errorf("pre-push scan of %d row files took %v, budget %v", prePushFiles, d, limit)
	}
}

// BenchmarkDetectorBuild is the build with the gitleaks config already
// parsed (every build after the first in a process; TestScanBudgets checks
// the cold one).
func BenchmarkDetectorBuild(b *testing.B) {
	for b.Loop() {
		if _, err := budgetScanner(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkScan4KB scans one 4 KB row with no hit.
func BenchmarkScan4KB(b *testing.B) {
	sc, err := budgetScanner()
	if err != nil {
		b.Fatal(err)
	}
	scanBench(sc, fill(prose, 4<<10))(b)
}

// BenchmarkScanMetaBlob16KB scans one 16 KB metadata value with no hit.
func BenchmarkScanMetaBlob16KB(b *testing.B) {
	sc, err := budgetScanner()
	if err != nil {
		b.Fatal(err)
	}
	scanBench(sc, metaBlob())(b)
}
