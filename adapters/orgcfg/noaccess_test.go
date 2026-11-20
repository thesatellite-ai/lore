package orgcfg

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// TestLinksSkipStoreWithoutAccess (devco D-726): an org lists a second
// store for everyone, but this login has no access to it (the forge
// answers "not a repository"). It made the whole entry unknown ("cannot
// read acme: …secret (never fetched)"), so the repo's own link in the
// first store was lost. Now the refused store is skipped: the links stay
// known, the store is listed once by NoAccessStores, a plain read does
// not ask it again, and the next sync (Always) tries it once more.
func TestLinksSkipStoreWithoutAccess(t *testing.T) {
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
	var routes routeLog // prefetch asks the router from several goroutines (D-774)
	route := routes.router(nil)
	gitRepo(t, "git@github.com:acme/app.git", "prj_app")
	sync := &GitLinks{Home: home, Config: cfg, Always: true, Route: route}
	res, err := sync.Links(ctx, "prj_app", []string{"github.com/acme/app"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != orgresolve.LinkStatusKnown || strings.Contains(res.Detail, "cannot read") {
		t.Fatalf("a store without access made the links %s: %s", res.Status, res.Detail)
	}
	got := NoAccessStores(home, cfg, nil)
	if len(got) != 1 || got[0].Store.URL != secretURL || got[0].Org != "acme" {
		t.Fatalf("NoAccessStores = %+v, want the secret store of acme", got)
	}
	if n := NoAccessNote(got[0]); !strings.HasPrefix(n, "secret: no access, skipped") || !strings.Contains(n, "admin of acme") {
		t.Errorf("note %q", n)
	}
	routes.reset()
	read := &GitLinks{Home: home, Config: cfg, Route: route}
	if res, err = read.Links(ctx, "prj_app", []string{"github.com/acme/app"}); err != nil || res.Status != orgresolve.LinkStatusKnown {
		t.Fatalf("plain read: %v %s %s", err, res.Status, res.Detail)
	}
	if strings.Contains(strings.Join(routes.asked(), " "), "secret") {
		t.Errorf("a plain read asked the refused store again: %q", routes.asked())
	}
	routes.reset()
	if _, err = sync.Links(ctx, "prj_app", []string{"github.com/acme/app"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(routes.asked(), " "), "secret") {
		t.Errorf("the next sync did not try the refused store again: %q", routes.asked())
	}
}

// TestNoAccessFault: refusals and a gone store skip; no answer is offline.
func TestNoAccessFault(t *testing.T) {
	cases := []struct {
		stderr string
		want   bool
	}{
		{"ERROR: Repository not found.\nfatal: Could not read from remote repository.", true},
		{"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", true},
		{"ssh: Could not resolve hostname github.com: Name or service not known", false},
		{"fatal: unable to access 'https://h/x.git/': Failed to connect to h port 443", false},
	}
	for _, c := range cases {
		k, got := noAccessFault(errors.New("orgcfg: git fetch: exit status 128: " + c.stderr))
		if got != c.want {
			t.Errorf("%q: skipped=%v (%s), want %v", c.stderr, got, k, c.want)
		}
	}
	if _, got := noAccessFault(nil); got {
		t.Error("nil error skipped")
	}
}
