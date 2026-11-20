package orgcfg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// TestGatherNeverTouchesAnotherOrgsStore (devco D-278): one laptop holds
// two orgs. A command in acme's repo (the active setup names acme) made
// a fetch and an ls-remote to beta's store, and read beta's clone, while
// resolving the org. Now beta's store is never asked for, nor cloned,
// unless nothing names the repo's org or beta's own patterns claim the
// repo (then its links still decide and fail closed, as before).
func TestGatherNeverTouchesAnotherOrgsStore(t *testing.T) {
	const betaURL = "git@github.com:beta/notes.git"
	cases := []struct {
		name      string
		setup     string // the active setup's org
		betaRepos string // beta's repos: pattern
		wantBeta  bool   // beta's store asked for
	}{
		{"the setup names acme: beta is never asked", "acme", "github.com/beta/*", false},
		{"nothing names the org: every store decides", "", "github.com/beta/*", true},
		{"beta's pattern claims the repo: beta is read", "acme", "github.com/acme/*", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			acmeURL, _ := sshWorld(t, storeOrigin(t))
			yml := "orgs:\n" +
				"  - name: acme\n    identity: {key: human:a@acme.example}\n    stores:\n      - url: " + acmeURL + "\n    own:\n      - github.com/acme/*\n" +
				"  - name: beta\n    identity: {key: human:b@beta.example}\n    stores:\n      - url: " + betaURL + "\n    repos:\n      - " + c.betaRepos + "\n"
			cfg, err := orgconf.Parse([]byte(yml))
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			writeFile(t, ConfigPath(home), yml)
			var routes routeLog // prefetch asks the router from several goroutines (D-774)
			route := routes.router(nil)
			links := &GitLinks{Home: home, Config: cfg, Always: true, Route: route}
			repo := gitRepo(t, "git@github.com:acme/app.git", "prj_app")
			s, err := Gather(ctx, In{Home: home, Dir: repo, Links: links, Setup: orgresolve.SetupHint{Name: "work", Org: c.setup}})
			if err != nil {
				t.Fatal(err)
			}
			betaNorm, err := repourl.NormalizeStore(betaURL)
			if err != nil {
				t.Fatal(err)
			}
			dir, _ := cloneDir(home, betaNorm)
			_, statErr := os.Stat(filepath.Join(dir, ".git"))
			asked := routes.asked()
			gotBeta := strings.Contains(strings.Join(asked, " "), "beta/") || statErr == nil
			if gotBeta != c.wantBeta {
				t.Fatalf("beta's store touched = %v (router asked %q, clone at %s: %v), want %v", gotBeta, asked, dir, statErr, c.wantBeta)
			}
			if !c.wantBeta && !strings.Contains(s.Result.LinkDetail, "another org's stores") {
				t.Errorf("link detail %q does not say beta's stores were not read", s.Result.LinkDetail)
			}
		})
	}
}
