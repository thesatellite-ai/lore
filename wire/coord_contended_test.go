package wire

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestCoordErrRacesLostIsContended (D-GRACE-B4): a signals write whose
// retries ran out on a reachable remote (OfflineError.RacesLost) is
// E_CONTENDED, exit 11, with a retry hint and no "reconnect" advice; every
// other OfflineError (unreachable, autopush off), wrapped or not, stays
// E_OFFLINE exit 15 with the never-queued hint.
func TestCoordErrRacesLostIsContended(t *testing.T) {
	t.Parallel()
	lost := &signals.OfflineError{Reason: "gave up after 6 lost races on refs/lore/signals (last seen 1234567)", RacesLost: true}
	cases := []struct {
		name      string
		err       error
		code      errs.Code
		exit      int
		says, not string
	}{
		{"races lost", lost, errs.Contended, 11, "retry", "reconnect"},
		{"races lost, wrapped", fmt.Errorf("claim: %w", lost), errs.Contended, 11, "lost races", "reconnect"},
		{"unreachable", &signals.OfflineError{Reason: "remote unreachable", Err: errors.New("dial tcp")}, errs.Offline, 15, "never queued", ""},
		{"autopush off", &signals.OfflineError{Reason: "autopush is off"}, errs.Offline, 15, "never queued", ""},
		{"unreachable, wrapped", fmt.Errorf("x: %w", &signals.OfflineError{Reason: "remote unreachable"}), errs.Offline, 15, "never queued", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var e *errs.Error
			if !errors.As(coordErr(tc.err), &e) {
				t.Fatalf("not an *errs.Error: %v", tc.err)
			}
			if e.Code != tc.code || errs.DefOf(e.Code).Exit != tc.exit {
				t.Fatalf("code %s exit %d, want %s %d", e.Code, errs.DefOf(e.Code).Exit, tc.code, tc.exit)
			}
			text := e.Message + " " + e.Hint
			if !strings.Contains(text, tc.says) {
				t.Fatalf("%q must say %q", text, tc.says)
			}
			if tc.not != "" && strings.Contains(text, tc.not) {
				t.Fatalf("%q must not say %q", text, tc.not)
			}
		})
	}
}
