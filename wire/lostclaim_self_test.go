package wire

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestLostClaimSelfHeldHint pins devco:D-896 (issue #121): an E_LOST_CLAIM
// whose claim on the ref is the caller's own actor's never tells it to stop
// work. In its own session (a claim the task no longer matches) the hint
// is the one claim that takes it back, with --push where claims need it;
// held in another session, the hint says so. Any other lost claim keeps
// the "stop work" hint.
func TestLostClaimSelfHeldHint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		push string
		says string
		not  string
	}{
		{"own session", &coord.FencedError{Kind: coord.FenceLost, Task: "tsk_1", Detail: "d", SelfHeld: true}, " --push", "`lore task claim --id tsk_1 --push` takes your claim on it again", "stop work"},
		{"own session, no push needed", &coord.FencedError{Kind: coord.FenceLost, Task: "tsk_1", Detail: "d", SelfHeld: true}, "", "`lore task claim --id tsk_1` takes", "stop work"},
		{"another session", &coord.FencedError{Kind: coord.FenceLost, Task: "tsk_1", Detail: "d", SelfHeld: true, OtherSession: "s-2"}, " --push", "you hold it in another session (s-2)", "stop work"},
		{"someone else", &coord.FencedError{Kind: coord.FenceLost, Task: "tsk_1", Detail: "d"}, " --push", "stop work on this task", "takes your claim"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var e *errs.Error
			if !errors.As(coordErrPush(tc.err, tc.push), &e) || e.Code != errs.LostClaim {
				t.Fatalf("want E_LOST_CLAIM, got %v", e)
			}
			if !strings.Contains(e.Hint, tc.says) || strings.Contains(e.Hint, tc.not) {
				t.Fatalf("hint %q must say %q and not %q", e.Hint, tc.says, tc.not)
			}
		})
	}
}
