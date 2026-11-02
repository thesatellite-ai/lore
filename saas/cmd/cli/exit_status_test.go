package main

import (
	"errors"
	"fmt"
	"testing"

	"saas/pkg/aicoder/errcodes"
)

// A failed command exits with its code's dedicated status even when the
// CLIError is wrapped by callers on the way up (fmt.Errorf %w is the norm).
func TestExitStatus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"bare coded", errcodes.New(errcodes.NoWork, "nothing ready"), 10},
		{"wrapped coded", fmt.Errorf("task next: %w", errcodes.New(errcodes.LostClaim, "lost")), 12},
		{"double wrapped", fmt.Errorf("a: %w", fmt.Errorf("b: %w", errcodes.New(errcodes.OrgAmbiguous, "two"))), 17},
		{"coded without own status", errcodes.New(errcodes.NotFound, "x"), errcodes.ExitGeneric},
		{"plain error", errors.New("boom"), errcodes.ExitGeneric},
	} {
		if got := exitStatus(tc.err); got != tc.want {
			t.Errorf("%s: exitStatus = %d, want %d", tc.name, got, tc.want)
		}
	}
}
