package profilestore

import (
	"slices"
	"testing"
)

// TestIsDirect: every listed op is direct, nothing else is (the existing
// actor verbs keep their own runners).
func TestIsDirect(t *testing.T) {
	names := OpNames()
	if len(names) != len(directOps) {
		t.Fatalf("OpNames %d != directOps %d", len(names), len(directOps))
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Errorf("%s listed twice", n)
		}
		seen[n] = true
		if !IsDirect(n) {
			t.Errorf("%s is not direct", n)
		}
	}
	for _, n := range []string{"actor.show", "actor.list", "actor.register", "actor.deactivate", "task.add", "project.list", ""} {
		if IsDirect(n) {
			t.Errorf("%s is direct", n)
		}
	}
	if !slices.Contains(names, OpActorOffboard) {
		t.Error("offboard missing")
	}
}
