package wire

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestOutsideNamesOrgSecondStore (devco:D-540): outside a repo an org's
// second store (an admins-only one `org store add` added) is named by its
// short name or as <org>/<store> by set-url and push alike; set-url
// refused "no org entry is named lore-ops … takes an org's name, me or a
// sto_ id". A short name two orgs share is refused naming both, and a
// name that is none lists every org's stores.
func TestOutsideNamesOrgSecondStore(t *testing.T) {
	const (
		team = "git@github.example:acme/lore-team.git"
		ops  = "git@github.example:acme/lore-ops.git"
		side = "git@github.example:side/lore-side.git"
		docs = "git@github.example:side/lore-docs.git"
		mine = "git@github.example:acme/lore-docs.git"
	)
	home := t.TempDir()
	body := "orgs:\n" +
		"  - name: acme\n    stores:\n      - url: " + team + "\n        org_folder: org\n      - url: " + ops + "\n        audience: admins\n      - url: " + mine + "\n" +
		"  - name: side\n    stores:\n      - url: " + side + "\n        org_folder: org\n      - url: " + docs + "\n"
	if err := os.WriteFile(configPath(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	a := openWith(t, t.TempDir(), Options{Home: home}) // a plain folder: no repo
	ctx := context.Background()
	for name, want := range map[string]string{"lore-ops": ops, "acme/lore-ops": ops, "acme": team, "side": side, "side/lore-docs": docs} {
		s, err := a.setURLSource(ctx, name)
		if err != nil || s.URL != want {
			t.Errorf("set-url --name %s = %q, %v; want %s", name, s.URL, err, want)
		}
		e, found, err := a.OutsideStoreNamed(ctx, "push", name)
		if err != nil || !found || e.URL != want {
			t.Errorf("push --store %s = %q, %v, %v; want %s", name, e.URL, found, err, want)
		}
	}
	if _, err := a.setURLSource(ctx, "lore-docs"); !errs.Has(err, errs.InvalidInput) ||
		!strings.Contains(err.Error(), "acme/lore-docs") || !strings.Contains(err.Error(), "side/lore-docs") {
		t.Errorf("a short name two orgs share: %v; want E_INVALID_INPUT naming acme/lore-docs and side/lore-docs", err)
	}
	_, err := a.setURLSource(ctx, "nosuch")
	if !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "acme/lore-ops") || !strings.Contains(err.Error(), "side/lore-side") {
		t.Errorf("an unknown name: %v; want E_NOT_FOUND listing every org's stores", err)
	}
	var le *errs.Error
	if strings.Contains(err.Error(), "sto_") || errors.As(err, &le) && strings.Contains(le.Hint, "sto_") {
		t.Errorf("the refusal offers a sto_ id, which store show and list never print: %v", err)
	}
	if _, found, err := a.OutsideStoreNamed(ctx, "forget", "some/dir"); found || err != nil {
		t.Errorf("a relative path no org has: found %v, %v; want it left as a path", found, err)
	}
}
