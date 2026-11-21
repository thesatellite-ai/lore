//go:build linux || darwin

package procwalk

import (
	"os"
	"testing"
)

// TestAsParentOfMovesTheWalk (devco:D-833): while the read server runs a
// command for a client, Parent and a walk from this process start at the
// client's parent, and the old answer comes back afterwards.
func TestAsParentOfMovesTheWalk(t *testing.T) {
	self := os.Getpid()
	restore := AsParentOf(self)
	if got := Parent(); got != self {
		restore()
		t.Fatalf("Parent() = %d, want the client's parent %d", got, self)
	}
	got := New().Ancestors(1)
	restore()
	if len(got) != 1 || got[0].PID != self {
		t.Fatalf("walk started at %v, want pid %d first", got, self)
	}
	if Parent() != os.Getppid() {
		t.Fatalf("Parent() = %d after restore, want %d", Parent(), os.Getppid())
	}
}
