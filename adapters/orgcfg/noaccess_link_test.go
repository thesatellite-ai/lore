package orgcfg

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// TestPickLinkStoreSkipsStoreWithoutAccess (devco D-726): a link fetches
// every store of the entry to find the one holding the folder. A store
// this login may not read (the forge refused it) used to fail the link as
// "store is offline (cannot tell which store holds …)". Now it is skipped
// without asking it again (the no-access record) and the link goes on in
// the store it can read; only a link that names the refused store with
// --store refuses, with ErrStoreNoAccess (not ErrStoreOffline).
func TestPickLinkStoreSkipsStoreWithoutAccess(t *testing.T) {
	const secretURL = "git@github.com:acme/secret.git"
	ctx := context.Background()
	acmeURL, _ := sshWorld(t, storeOrigin(t))
	yml := "orgs:\n  - name: acme\n    identity: {key: human:a@acme.example}\n    stores:\n      - url: " + acmeURL +
		"\n      - url: " + secretURL + "\n    own:\n      - github.com/acme/*\n"
	cfg, err := orgconf.Parse([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	writeFile(t, ConfigPath(home), yml)
	var asked []string
	route := func(_ context.Context, u string) ([]string, []string) {
		asked = append(asked, u)
		return nil, nil
	}
	acmeNorm, _ := repourl.NormalizeStore(acmeURL)
	secretNorm, _ := repourl.NormalizeStore(secretURL)
	g := &GitLinks{Home: home, Config: cfg, Route: route}

	// first link: the refused fetch is classified, recorded and skipped
	got, err := g.pickLinkStore(ctx, orgresolve.LinkPlan{Entry: "acme", StoreURL: acmeNorm, Folder: "app"})
	if err != nil || got != acmeNorm {
		t.Fatalf("pickLinkStore = %q, %v; want %s (the refused store skipped)", got, err, acmeNorm)
	}
	if len(NoAccessStores(home, cfg, nil)) != 1 {
		t.Fatal("the refused store was not recorded")
	}
	// next link: no fetch of the refused store at all
	asked = nil
	if got, err = g.pickLinkStore(ctx, orgresolve.LinkPlan{Entry: "acme", StoreURL: acmeNorm, Folder: "app"}); err != nil || got != acmeNorm {
		t.Fatalf("second pickLinkStore = %q, %v", got, err)
	}
	if strings.Contains(strings.Join(asked, " "), "secret") {
		t.Errorf("a recorded refusal was fetched again: %q", asked)
	}
	// --store names the refused store: the true cause
	_, err = g.pickLinkStore(ctx, orgresolve.LinkPlan{Entry: "acme", StoreURL: secretNorm, Folder: "app", StoreExplicit: true})
	if !errors.Is(err, ErrStoreNoAccess) || errors.Is(err, ErrStoreOffline) {
		t.Fatalf("err = %v, want ErrStoreNoAccess", err)
	}
}
