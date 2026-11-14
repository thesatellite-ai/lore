package wire

import (
	"errors"
	"fmt"
	"strings"
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
	// G-16: a store that answers "no repository" moved or was deleted:
	// the hint names set-url and the restore, never "retry"
	gone := linkError(fmt.Errorf("%w: git.example.test/acme/notes ('/srv/notes.git' does not appear to be a git repository)", orgcfg.ErrStoreOffline))
	if h := errs.EnvelopeOf(gone).Error.Hint; !strings.Contains(h, "lore store set-url --name <org>") || !strings.Contains(h, "--restore --yes") || strings.Contains(h, "retry") {
		t.Fatalf("gone store hint: %q", h)
	}
	if strings.Contains(gone.Error(), "is offline") {
		t.Fatalf("a gone store is called offline: %v", gone)
	}
	// org create goes through orgFileError first: its own sentinels, then linkError.
	if c := errs.CodeOf(orgFileError(wrap(orgcfg.ErrStoreRefused), orgTestStoreURL)); c != errs.PolicyDenied {
		t.Fatalf("org create on a declining store: %s", c)
	}
	if c := errs.CodeOf(orgFileError(wrap(orgcfg.ErrOrgFileExists), orgTestStoreURL)); c != errs.InvalidInput {
		t.Fatalf("org create over another org: %s", c)
	}
}

// TestNoStoreBranchHintNamesStoreInit: the refusal of a link to a store
// branch that does not exist names the explicit way to start one (the
// docs' fix, and Q8: lore never makes it by accident), not only the
// typo check.
func TestNoStoreBranchHintNamesStoreInit(t *testing.T) {
	t.Parallel()
	err := linkError(fmt.Errorf("%w: github.com/example/notes has no branch staging", orgcfg.ErrNoStoreBranch))
	if h := errs.EnvelopeOf(err).Error.Hint; !strings.Contains(h, "lore store init --name <name> --url '<url>?branch=<branch>'") {
		t.Fatalf("hint %q does not name lore store init with the branch", h)
	}
}
