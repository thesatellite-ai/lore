package main

import (
	"errors"
	"fmt"
	"testing"

	coreerrs "github.com/thesatellite-ai/lore/model/errs"

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
		// Q-R2-5: every code exits its core class exit; exit 1 only for
		// E_INTERNAL, unregistered codes and errors with no lore code.
		{"not found", errcodes.New(errcodes.NotFound, "x"), 27},
		{"folded project not found (DOCS O4)", errcodes.New(errcodes.ProjectNotFound, "x"), 27},
		{"unsupported is refused", errcodes.New(errcodes.Unsupported, "no"), 23},
		{"secret detected is refused", errcodes.New(errcodes.SecretDetected, "no"), 23},
		{"core needs confirm", coreerrs.New(coreerrs.NeedsConfirm, "ask"), 26},
		{"wrapped core not found", fmt.Errorf("x: %w", coreerrs.New(coreerrs.NotFound, "gone")), 27},
		{"old wraps core: outer decides", errcodes.New(errcodes.InvalidInput, "bad").WithCause(coreerrs.New(coreerrs.NotFound, "gone")), 20},
		{"core wraps old: outer decides", coreerrs.Wrap(coreerrs.NeedsConfirm, "ask", errcodes.New(errcodes.NotFound, "x")), 26},
		{"internal", errcodes.New(errcodes.Internal, "bug"), errcodes.ExitGeneric},
		{"unregistered code", errcodes.New(errcodes.Code("E_NOT_REGISTERED"), "x"), errcodes.ExitGeneric},
		{"plain error", errors.New("boom"), errcodes.ExitGeneric},
		// FLAG2: every bad_input code exits 20 on the old commands too.
		{"invalid input", errcodes.New(errcodes.InvalidInput, "bad"), 20},
		{"invalid id (stays E_INVALID_ID, BATCH2 #11)", errcodes.New(errcodes.InvalidID, "bad id"), 20},
		{"wrapped invalid input", fmt.Errorf("x: %w", errcodes.New(errcodes.EmptyBody, "empty")), 20},
		{"refused (BATCH2 #8)", errcodes.New(errcodes.Refused, "no"), 23},
	} {
		if got := exitStatus(tc.err); got != tc.want {
			t.Errorf("%s: exitStatus = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// EXACT ERRORS: a core error that reaches the old path keeps its hint line,
// as the core surface prints it; the outer error's hint wins.
func TestErrorHint_CoreError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"old", errcodes.New(errcodes.NotFound, "x").WithHint("run a"), "run a"},
		{"core", fmt.Errorf("w: %w", coreerrs.New(coreerrs.NeedsConfirm, "ask").WithHint("rerun with --yes")), "rerun with --yes"},
		{"plain", errors.New("boom"), ""},
	} {
		if got := errorHint(tc.err); got != tc.want {
			t.Errorf("%s: errorHint = %q, want %q", tc.name, got, tc.want)
		}
	}
}
