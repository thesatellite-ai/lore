package wire

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestSignalsErrorsNameTheirCause (devco:D-967): every signals write error
// a migrate team lock or a coordination command can meet prints a lore
// code and its cause, never the bare "[E_INTERNAL] internal error" a racing
// `lore migrate run --team-lock --push` printed when the remote refused the
// write for a reason this lore did not know.
func TestSignalsErrorsNameTheirCause(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	rejected := fmt.Errorf("%w: refs/lore/signals remote-rejected: [remote rejected] (pre-receive hook declined)", signals.ErrRejected)
	partial := fmt.Errorf("%w: companion 1: %w", signals.ErrPartial, errors.New("push refs/heads/x refused"))
	raced := &signals.OfflineError{Reason: "gave up after 8 lost races over 2s on refs/lore/signals", RacesLost: true}
	cases := []struct {
		name string
		got  error
		code errs.Code
		says string
	}{
		{"migrate: remote refused", migrateLockError(rejected, now), errs.Refused, "pre-receive hook declined"},
		{"migrate: lost every race", migrateLockError(raced, now), errs.Contended, "gave up after 8 lost races"},
		{"migrate: partial", migrateLockError(partial, now), errs.Internal, "companion 1"},
		{"migrate: anything else", migrateLockError(errors.New("signals: build commit: disk full"), now), errs.Internal, "disk full"},
		{"coord: remote refused", coordErr(rejected), errs.Refused, "pre-receive hook declined"},
		{"coord: partial", coordErr(partial), errs.Internal, "companion 1"},
	}
	for _, c := range cases {
		env := errs.EnvelopeOf(c.got).Error
		if env.Code != c.code {
			t.Errorf("%s: code %s, want %s (%q)", c.name, env.Code, c.code, env.Message)
		}
		if !strings.Contains(env.Message, c.says) {
			t.Errorf("%s: message %q does not name the cause %q", c.name, env.Message, c.says)
		}
	}
}
