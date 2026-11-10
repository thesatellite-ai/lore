package setup

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestNotFoundHasHint: E_SETUP_NOT_FOUND from a setup command always
// prints a hint line (the text form shows the hint, never next): with no
// setups it names the guided setup, with some it names them in the
// message (the machine sweep found `lore setup show main` on a machine
// with no setups printing "shows the 0 setups" and no hint).
func TestNotFoundHasHint(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  onboard.Config
		msg  string
		hint string
	}{
		{"none", onboard.Config{}, "this machine has no setups", "lore setup makes one"},
		{"two", onboard.Config{Setups: []onboard.SetupEntry{{Name: "personal"}, {Name: "acme-hub"}}}, "the setups on this machine: personal, acme-hub", "lore setup list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var e *errs.Error
			if !errors.As(notFound("main", tc.cfg), &e) || e.Code != errs.SetupNotFound {
				t.Fatalf("not E_SETUP_NOT_FOUND: %v", e)
			}
			if !strings.Contains(e.Message, tc.msg) || strings.Contains(e.Message, " 0 setups") {
				t.Errorf("message %q, want it to say %q", e.Message, tc.msg)
			}
			if !strings.HasPrefix(e.Hint, tc.hint) {
				t.Errorf("hint %q, want %q", e.Hint, tc.hint)
			}
		})
	}
}
