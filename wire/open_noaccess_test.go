package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestOrgNoAccessErrorNamesTheOrg (devco D-726): a store open the forge
// refused, for a store an org of this machine lists, says "no access to
// <store>", who to ask and the one line to run after; never "go online".
// An offline failure, or a store no org lists, keeps its own error.
func TestOrgNoAccessErrorNamesTheOrg(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, aliasOrgConfig)
	const url = "git@git.example.org:acme/notes.git"
	refused := errs.Wrap(errs.StoreUnreachable, "git: the store has no local copy yet, and its remote refused this machine's access", git.ErrNoAccessRecorded)

	got := errs.EnvelopeOf(orgNoAccessError(home, url, refused)).Error
	if got.Code != errs.StoreUnreachable {
		t.Errorf("code = %s, want E_STORE_UNREACHABLE", got.Code)
	}
	for _, want := range []string{"no access to notes", "git.example.org:acme/notes.git"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("message %q lacks %q", got.Message, want)
		}
	}
	for _, want := range []string{"Ask an admin of acme", "`lore sync`", "nothing written"} {
		if !strings.Contains(got.Hint, want) {
			t.Errorf("hint %q lacks %q", got.Hint, want)
		}
	}
	if strings.Contains(got.Message+got.Hint, "go online") {
		t.Errorf("refused store told to go online: %q / %q", got.Message, got.Hint)
	}

	offline := errs.New(errs.StoreUnreachable, "git: the store has no local copy yet and cannot be reached")
	if e := orgNoAccessError(home, url, offline); e != offline {
		t.Errorf("an offline store's error was rewritten: %v", e)
	}
	if e := orgNoAccessError(home, "git@git.example.org:other/vault.git", refused); e != refused {
		t.Errorf("a store no org lists was rewritten: %v", e)
	}
}
