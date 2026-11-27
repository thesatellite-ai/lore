package orgcfg

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestReadsFetchEveryOrgInParallel (devco:D-1026, H-290): the first read of
// a session fetched the stores of one org at the same time (M-4) but the
// orgs one after another, so a person in two orgs (one store each) paid two
// network round trips before lore answered. With a remote that takes 1 s
// per call, a due read of two orgs takes about one call, still one fetch
// per store; an org the read does not look at (Only) is not fetched.
func TestReadsFetchEveryOrgInParallel(t *testing.T) {
	root := t.TempDir()
	hosts := filepath.Join(root, "hosts")
	gc := filepath.Join(root, "gitconfig")
	writeFile(t, gc, "[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n[url \""+filepath.ToSlash(hosts)+"/\"]\n\tinsteadOf = https://github.com/\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(envAutopush, "")
	yaml := "orgs:\n"
	for i, org := range []string{"acme", "widgets", "globex"} {
		origin := filepath.Join(hosts, org, "lore.git")
		if err := os.MkdirAll(origin, 0o700); err != nil {
			t.Fatal(err)
		}
		testGit(t, origin, "init", "-q", "--bare", "-b", "main")
		seed := filepath.Join(root, "seed-"+org)
		url := "https://github.com/" + org + "/lore"
		testGit(t, root, "clone", "-q", url, seed)
		writeFile(t, filepath.Join(seed, storeMarkerFile), "lore_store: 2\nid: sto_01a1a5aca7f57659be7320c5f4b701"+string(rune('0'+i))+"0\n")
		testGit(t, seed, "add", ".")
		testGit(t, seed, "commit", "-q", "-m", "init")
		testGit(t, seed, "push", "-q", "origin", "main")
		yaml += "  - name: " + org + "\n    stores:\n      - url: " + url + "\n"
	}
	cfg, err := orgconf.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	slow := func(context.Context, string) ([]string, []string) {
		calls.Add(1)
		time.Sleep(slowRemoteDelay)
		return nil, nil
	}
	now := time.Now()
	g := &GitLinks{Home: filepath.Join(root, "home"), Config: cfg, Route: slow, Now: func() time.Time { return now },
		Only: map[string]bool{"acme": true, "widgets": true}}
	ctx := context.Background()
	const project = "prj_01a1a5aca7f57659be7320c5f4b70099"
	remotes := []string{"github.com/acme/app"}

	// first read on this machine: clones, fetches and HEAD branches recorded
	if _, err := g.Links(ctx, project, remotes); err != nil {
		t.Fatal(err)
	}
	calls.Store(0)

	// the first read of the next session: every fetch is due
	now = now.Add(orgconf.DefaultFetchTTL + time.Second)
	start := time.Now()
	if _, err := g.Links(ctx, project, remotes); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if n := calls.Load(); n != 2 {
		t.Errorf("a due read of two orgs made %d network calls, want 2 (one fetch per store of the orgs it reads, none of globex)", n)
	}
	if took >= 2*slowRemoteDelay {
		t.Errorf("a due read of two orgs (one store each) took %s, want about one call (%s): the orgs were fetched one after another", took, slowRemoteDelay)
	}
}
