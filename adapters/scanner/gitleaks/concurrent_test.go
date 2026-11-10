package gitleaks

import (
	"reflect"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
)

// TestScanConcurrent: one Scanner shared by many goroutines (lint's
// parallel row scan, P3-PERF-1) gives every caller the same report a scan
// on its own gives, and `go test -race` finds no race in the shared
// gitleaks detector. Fixtures are built at run time (push protection).
func TestScanConcurrent(t *testing.T) {
	t.Parallel()
	s := full(t)
	texts := []string{
		"glpat-" + "Xk9Lm2Qp7Rt4Vw8Yz1Ab",
		"key " + "AKIA" + "Q7Z2X9KL0PM4N3B8" + " here",
		"token ghp_" + rep("aB3", 12),
		"plain note with nothing in it",
		`{"note":"bench 1","id":"tme_` + rep("0a1b", 8) + `"}`,
	}
	want := make([]scan.Report, len(texts))
	for i, txt := range texts {
		want[i] = s.ScanReport("body", txt)
	}
	for i := range 3 {
		if len(want[i].Findings) == 0 {
			t.Fatalf("fixture %d finds nothing alone: the test would prove nothing", i)
		}
	}
	const workers = 16
	var wg sync.WaitGroup
	got := make([][]scan.Report, workers)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				for _, txt := range texts {
					got[w] = append(got[w], s.ScanReport("body", txt))
				}
			}
		}()
	}
	wg.Wait()
	for w := range workers {
		for k, r := range got[w] {
			if i := k % len(texts); !reflect.DeepEqual(r, want[i]) {
				t.Fatalf("worker %d text %d: concurrent report %+v, alone %+v", w, i, r, want[i])
			}
		}
	}
}
