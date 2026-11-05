package main

import (
	"bytes"
	"strings"
	"testing"

	"saas/pkg/aicoder/live"
)

// TestPrintLandOutcome_NamesRealBranch pins that every landing message names
// the branch the pass landed on. A repo whose default_branch: is not main
// once read "landed on main" after landing on its own branch.
func TestPrintLandOutcome_NamesRealBranch(t *testing.T) {
	const branch = "trunk"
	statuses := []live.LandStatus{live.LandDone, live.LandProtected, live.LandRetriesOut, live.LandRefused}
	for _, st := range statuses {
		for _, left := range []int{0, 2} {
			out := landOutcome{Branch: branch, Status: st, Commit: "0123456789abcdef", Detail: "hook said no", Left: left}
			var buf bytes.Buffer
			if err := printLandOutcome(&buf, out); err != nil {
				t.Fatal(err)
			}
			got := buf.String()
			if !strings.Contains(got, branch) {
				t.Errorf("status %s left %d: %q does not name %q", st, left, got, branch)
			}
			if strings.Contains(got, "main") {
				t.Errorf("status %s left %d: %q names main, but the branch is %q", st, left, got, branch)
			}
		}
	}

	// A zero outcome (no branch recorded) never claims "main" either.
	var buf bytes.Buffer
	if err := printLandOutcome(&buf, landOutcome{Status: live.LandDone, Commit: "0123456789abcdef", Left: 1}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "main") {
		t.Errorf("zero-branch outcome %q names main", buf.String())
	}
}
