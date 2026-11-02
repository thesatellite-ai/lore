package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/queue"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
)

// TestCoordErrorExitCodes pins the exit statuses agents branch on (§1
// "Error codes"): every coordination failure maps to its own code, wrapped
// or not.
func TestCoordErrorExitCodes(t *testing.T) {
	held := signals.Signal{Kind: signals.KindClaim, Task: "msn_1", Holder: "act_c", HolderLabel: "agent:coord-1", Session: "s", ClaimToken: "ct", Expires: time.Now()}
	cases := []struct {
		name string
		err  error
		code errcodes.Code
		exit int
		says string
	}{
		{"lost", &coord.FencedError{Kind: coord.FenceLost, Task: "tsk_1", Detail: "gone"}, errcodes.LostClaim, 12, ""},
		{"cancelled", &coord.FencedError{Kind: coord.FenceCancelled, Task: "tsk_1"}, errcodes.Cancelled, 13, "cancelled"},
		{"reassigned", &coord.FencedError{Kind: coord.FenceReassigned, Task: "tsk_1"}, errcodes.Reassigned, 14, "reassigned"},
		{"wrapped lost", fmt.Errorf("ctx: %w", &coord.FencedError{Kind: coord.FenceLost, Task: "tsk_1"}), errcodes.LostClaim, 12, ""},
		{"offline", &signals.OfflineError{Reason: "remote unreachable"}, errcodes.Offline, 15, "never queued"},
		{"no work", &coord.NoWorkError{Waiting: []queue.Waiting{{Task: queue.Task{ID: "tsk_2"}, Reason: "waits for tsk_1 to be merged"}}}, errcodes.NoWork, 10, "waits for tsk_1"},
		{"contended", coord.ErrContended, errcodes.Contended, 11, ""},
		{"bare not held", fmt.Errorf("x: %w", signals.ErrNotHeld), errcodes.LostClaim, 12, ""},
		{"mission held", &signals.HeldError{By: held}, errcodes.InvalidInput, 1, "coordinated by coord-1"},
		{"same actor", &signals.HeldError{By: held, SameActor: true}, errcodes.InvalidInput, 1, "LORE_ACTOR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cli *errcodes.CLIError
			if !errors.As(coordError(tc.err), &cli) {
				t.Fatalf("not a CLIError: %v", coordError(tc.err))
			}
			if cli.Code != tc.code || errcodes.ExitCode(cli.Code) != tc.exit {
				t.Fatalf("code %s exit %d, want %s %d", cli.Code, errcodes.ExitCode(cli.Code), tc.code, tc.exit)
			}
			if tc.says != "" && !strings.Contains(cli.Message+" "+cli.Hint, tc.says) {
				t.Fatalf("message %q / hint %q must say %q", cli.Message, cli.Hint, tc.says)
			}
		})
	}
	if coordError(nil) != nil {
		t.Fatal("nil stays nil")
	}
}
