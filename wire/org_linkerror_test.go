package wire

import (
	"errors"
	"fmt"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestLinkErrorMatrix: every store-write error of `lore org link` / `org
// create` maps to its lore code (wrapped as the writer wraps them), a
// lore error keeps its own code, anything else is E_INTERNAL; the
// refusal and the lost race carry their hints.
func TestLinkErrorMatrix(t *testing.T) {
	t.Parallel()
	wrap := func(e error) error { return fmt.Errorf("%w: github.com/agency/notes main: detail", e) }
	cases := []struct {
		name string
		err  error
		want errs.Code
		hint bool
	}{
		{"not a store", wrap(orgcfg.ErrNotAStore), errs.NotAStore, true},
		{"no branch", wrap(orgcfg.ErrNoStoreBranch), errs.InvalidInput, true},
		{"offline", wrap(orgcfg.ErrStoreOffline), errs.Offline, true},
		{"folder elsewhere", wrap(orgcfg.ErrFolderElsewhere), errs.InvalidInput, true},
		{"store refused (hook)", wrap(orgcfg.ErrStoreRefused), errs.PolicyDenied, true},
		{"race lost every time", wrap(orgcfg.ErrLinkRace), errs.BusyTimeout, true},
		{"a lore error keeps its code", errs.New(errs.StoreLocked, "busy"), errs.StoreLocked, false},
		{"anything else", errors.New("disk on fire"), errs.Internal, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := linkError(c.err)
			if errs.CodeOf(got) != c.want {
				t.Fatalf("code %s, want %s (%v)", errs.CodeOf(got), c.want, got)
			}
			if h := errs.EnvelopeOf(got).Error.Hint; c.hint && h == "" {
				t.Fatalf("no hint on %v", got)
			}
		})
	}
	// org create goes through orgFileError first: its own sentinels, then linkError.
	if c := errs.CodeOf(orgFileError(wrap(orgcfg.ErrStoreRefused), orgTestStoreURL)); c != errs.PolicyDenied {
		t.Fatalf("org create on a declining store: %s", c)
	}
	if c := errs.CodeOf(orgFileError(wrap(orgcfg.ErrOrgFileExists), orgTestStoreURL)); c != errs.InvalidInput {
		t.Fatalf("org create over another org: %s", c)
	}
}
