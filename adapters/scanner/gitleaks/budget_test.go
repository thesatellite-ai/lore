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
// table, as set by OWNERQ-13) are the contract; a change that makes the
// scanner slower than them fails here instead of in a person's commit hook.
//
//	scan part of a pre-push over 1,000 changed row files      < 2 s    TestScanBudgets
//	detector build (defaults + lore pack + store rules), cold  < 60 ms  BenchmarkDetectorBuild (warm) only
//	scan of a 4 KB row, no hit                                 < 5 ms   BenchmarkScan4KB, BenchmarkScan4KBCandidates
//	scan of a 16 KB metadata blob (the MQ3 cap)                < 20 ms  BenchmarkScanMetaBlob16KB only
//
// Only the pre-push budget is asserted (OWNERQ-13): per-row times swing
// with machine load, so an assertion would flap; the benchmarks keep the
// cost visible (go test -bench . ./adapters/scanner/gitleaks). Detection
// is never weakened to meet a number: gitleaks' decode depth 3 and its
// keyword-free rules stay. The decode passes are skipped only for a text
// mayHoldEncoding proves has no encoded segment (encoded.go), which is
// most prose and metadata; a row holding paths or long identifiers pays
// them (BenchmarkScan4KBCandidates). The "zero for commands that write no
// text" row is a wiring property (wire withLazyBuild, P3-SEC-5), not a
// timing.
//
// The timed case takes the fastest of budgetRuns runs (race_test.go,
// norace_test.go), each timed in process CPU time, not wall time
// (cpuClock, cputime_unix_test.go): a run that waits for a core on a busy
// machine uses no CPU while it waits, so the check is about the scanner,
// not the load.
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

// clockEpoch anchors wallClock.
var clockEpoch = time.Now()

// wallClock is the wall time since clockEpoch, the fallback of cpuClock.
func wallClock() time.Duration { return time.Since(clockEpoch) }

// fastest is the fastest of budgetRuns calls of f, each timed on the
// process CPU clock (cpuClock), so a loaded machine does not fail it.
func fastest(f func()) time.Duration {
	best := time.Duration(0)
	for range budgetRuns {
		start := cpuClock()
		f()
		if d := cpuClock() - start; best == 0 || d < best {
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
		t.Errorf("pre-push scan of %d row files took %v of CPU time, budget %v", prePushFiles, d, limit)
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

// BenchmarkScan4KBCandidates scans a 4 KB row with no hit whose text
// holds codec candidates (paths, long identifiers), so the decode passes
// run: the slow side of the 3.8 per-row figure.
func BenchmarkScan4KBCandidates(b *testing.B) {
	sc, err := budgetScanner()
	if err != nil {
		b.Fatal(err)
	}
	scanBench(sc, fill("See github.com/thesatellite-ai/lore/adapters/scanner for the release_checklist_items. "+prose, 4<<10))(b)
}

// BenchmarkScanMetaBlob16KB scans one 16 KB metadata value with no hit.
func BenchmarkScanMetaBlob16KB(b *testing.B) {
	sc, err := budgetScanner()
	if err != nil {
		b.Fatal(err)
	}
	scanBench(sc, metaBlob())(b)
}
