package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
)

// TestApplyingSignerSignsTheNewOrgStore (F-16): while an admin team step
// creates org B's store, the clone gets the step's identity in B (the org
// entry does not exist yet), for any spelling of the store url; another
// store, or a ctx with no step, does not.
func TestApplyingSignerSignsTheNewOrgStore(t *testing.T) {
	t.Parallel()
	const storeURL = "git@git.example.test:beta/lore-store.git"
	sig := git.Signature{Name: "Mira Castell", Email: "mira@beta.example"}
	s := storeSigner{opts: Options{Home: t.TempDir()}}
	ctx := withApplyingSigner(withApplyingSetup(background(), "beta", "beta"), storeURL, sig)
	for _, remote := range []string{storeURL, "ssh://git@git.example.test/beta/lore-store", "git@git.example.test:beta/lore-store.git?branch=main"} {
		if got := s.cloneIdentity(ctx, remote); got.Signature != sig {
			t.Errorf("clone identity of %s = %+v, want %+v", remote, got.Signature, sig)
		}
		if got := s.identityFor(ctx, remote); got != sig {
			t.Errorf("identityFor %s = %+v, want %+v", remote, got, sig)
		}
	}
	if got, ok := applyingSigner(ctx, "git@git.example.test:alpha/lore-store.git"); ok {
		t.Errorf("another store got the step's signer %+v", got)
	}
	if _, ok := applyingSigner(background(), storeURL); ok {
		t.Error("a ctx with no setup step named a signer")
	}
	if _, ok := applyingSigner(withApplyingSigner(background(), storeURL, sig), storeURL); ok {
		t.Error("withApplyingSigner outside a setup step set a signer")
	}
}
