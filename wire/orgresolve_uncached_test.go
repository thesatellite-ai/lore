package wire

import (
	"context"
	"testing"
)

// TestUncachedSituationKeepsNothing is devco:D-388-init: lore init's
// client-repo question gathers the org situation before init sets the
// project up; that answer must not stay for init's own writes (in a
// monorepo package it named the root's project, and the new project's
// repos row was refused as "a row of another project"). A Situation call
// under withUncachedSituation keeps nothing; a plain call still keeps its
// answer for the run.
func TestUncachedSituationKeepsNothing(t *testing.T) {
	t.Parallel()
	r := NewOrgResolution(t.TempDir(), t.TempDir(), nil)
	ctx := context.Background()
	if _, err := r.Situation(withUncachedSituation(ctx)); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	kept := r.done || r.sit != nil
	r.mu.Unlock()
	if kept {
		t.Fatal("an uncached Situation call kept its answer for later calls")
	}
	if _, err := r.Situation(ctx); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	kept = r.done
	r.mu.Unlock()
	if !kept {
		t.Fatal("a plain Situation call must keep its answer for the run")
	}
}
