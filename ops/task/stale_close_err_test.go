package task

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// A cancel that fails part way through task stale --close prints the
// batch context and then the cancel's own reason, code and hint; a
// non-lore failure keeps its text hidden (errs.Prefix).
func TestStaleCloseErrKeepsReason(t *testing.T) {
	t.Parallel()
	const id, batch = "tsk_01example", "bat_01example"
	ctxLine := "task stale --close: " + id + " failed after earlier tasks of batch " + batch + " were cancelled"
	for _, tc := range []struct {
		name      string
		err       error
		code      errs.Code
		msg, hint string
	}{
		{"lore refusal", errs.New(errs.PolicyDenied, "the task is locked by acme").WithHint("lore task unlock " + id),
			errs.PolicyDenied, ctxLine + ": the task is locked by acme", "lore task unlock " + id},
		{"plain error", errors.New("disk: /var/x"), errs.Internal, ctxLine, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := errs.EnvelopeOf(staleCloseErr(id, batch, tc.err)).Error
			if b.Code != tc.code || b.Message != tc.msg || b.Hint != tc.hint {
				t.Fatalf("got %s %q hint %q, want %s %q hint %q", b.Code, b.Message, b.Hint, tc.code, tc.msg, tc.hint)
			}
			if strings.Contains(b.Message, "/var/x") {
				t.Fatalf("a non-lore cause reached the message: %q", b.Message)
			}
		})
	}
}
