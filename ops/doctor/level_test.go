package doctor

import (
	"context"
	"testing"
)

// TestErrorFindingBreaks: an error finding of a core check (a damaged
// cache) goes to Errors and breaks the status (exit 2) from healthy and
// from degraded; a fixed one is a note and changes nothing.
func TestErrorFindingBreaks(t *testing.T) {
	bad := Finding{Check: "cache", Level: LevelError, Line: "cache x is damaged; delete it"}
	for _, base := range Healths() {
		h := healthy()
		h.Status = base
		env := &fakeEnv{host: &fakeHost{base: h}, runs: []CheckRun{{Check: "cache", Findings: []Finding{bad}}}}
		r, err := Run(context.Background(), env, RunIn{})
		if err != nil {
			t.Fatal(err)
		}
		// A broken base with no error of its own adds its unnamed line (unnamedBase) before the finding.
		want := 1
		if base == StatusBroken {
			want = 2
		}
		if r.Status != StatusBroken || r.Exit != exitBroken || len(r.Errors) != want || r.Errors[want-1] != bad.Line || r.OK {
			t.Fatalf("base %s: %+v", base, r)
		}
	}
	fixed := bad
	fixed.Fixed = true
	env := &fakeEnv{host: &fakeHost{base: healthy()}, runs: []CheckRun{{Check: "cache", Findings: []Finding{fixed}}}}
	r, err := Run(context.Background(), env, RunIn{})
	if err != nil || r.Status != StatusHealthy || len(r.Errors) != 0 || len(r.Notes) != 1 {
		t.Fatalf("fixed error: %+v %v", r, err)
	}
}
