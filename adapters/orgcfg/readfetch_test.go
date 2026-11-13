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

// slowRemoteDelay is what one network call costs in TestReadsFetchOnceInParallel
// (an ssh round trip to a far git host).
const slowRemoteDelay = time.Second

// TestReadsFetchOnceInParallel (owner item M-4): a read on a Mac took ~25 s,
// one ~3 s ssh call after another (per org store a fetch and an ls-remote
// of its HEAD branch). With three org stores behind a remote that takes 1 s
// per call: a cold read fetches the three at the same time and asks no
// store for its HEAD branch again once recorded (about one call's time, not
// six); a second read within live.fetch_ttl makes no network call at all;
// lore sync (Always) fetches every store and asks each for its HEAD branch.
func TestReadsFetchOnceInParallel(t *testing.T) {
	root := t.TempDir()
	hosts := filepath.Join(root, "hosts")
	gc := filepath.Join(root, "gitconfig")
	writeFile(t, gc, "[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n[url \""+filepath.ToSlash(hosts)+"/\"]\n\tinsteadOf = https://github.com/\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(envAutopush, "")
	yaml := "orgs:\n  - name: agency\n    stores:\n"
	for _, name := range []string{"one", "two", "three"} {
		origin := filepath.Join(hosts, "agency", name+".git")
		if err := os.MkdirAll(origin, 0o700); err != nil {
			t.Fatal(err)
		}
		testGit(t, origin, "init", "-q", "--bare", "-b", "main")
		seed := filepath.Join(root, "seed-"+name)
		url := "https://github.com/agency/" + name
		testGit(t, root, "clone", "-q", url, seed)
		writeFile(t, filepath.Join(seed, storeMarkerFile), "lore_store: 2\nid: sto_01a1a5aca7f57659be7320c5f4b700"+map[string]string{"one": "01", "two": "02", "three": "03"}[name]+"\n")
		testGit(t, seed, "add", ".")
		testGit(t, seed, "commit", "-q", "-m", "init")
		testGit(t, seed, "push", "-q", "origin", "main")
		yaml += "      - url: " + url + "\n"
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
	g := &GitLinks{Home: filepath.Join(root, "home"), Config: cfg, Route: slow, Now: func() time.Time { return now }}
	ctx := context.Background()

	// first read on this machine: every store is cloned and fetched, and
	// its HEAD branch asked once (nothing recorded yet)
	if _, err := g.Links(ctx, "prj_01a1a5aca7f57659be7320c5f4b70099", []string{"github.com/agency/app"}); err != nil {
		t.Fatal(err)
	}
	first := calls.Swap(0)

	// a read after live.fetch_ttl: due again, fetched in parallel, no ls-remote
	now = now.Add(orgconf.DefaultFetchTTL + time.Second)
	start := time.Now()
	if _, err := g.Links(ctx, "prj_01a1a5aca7f57659be7320c5f4b70099", []string{"github.com/agency/app"}); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if n := calls.Swap(0); n != 3 {
		t.Errorf("a due read made %d network calls, want 3 (one fetch per store, no ls-remote; the first read made %d)", n, first)
	}
	if took >= 2*slowRemoteDelay {
		t.Errorf("a due read of 3 stores took %s, want about one call (%s): the fetches ran one after another", took, slowRemoteDelay)
	}

	// a second read within live.fetch_ttl: no network at all
	now = now.Add(time.Minute)
	if _, err := g.Links(ctx, "prj_01a1a5aca7f57659be7320c5f4b70099", []string{"github.com/agency/app"}); err != nil {
		t.Fatal(err)
	}
	if n := calls.Swap(0); n != 0 {
		t.Errorf("a read within live.fetch_ttl made %d network calls, want 0", n)
	}

	// lore sync: every store fetched and asked for its HEAD branch
	g.Always = true
	if _, err := g.Links(ctx, "prj_01a1a5aca7f57659be7320c5f4b70099", []string{"github.com/agency/app"}); err != nil {
		t.Fatal(err)
	}
	if n := calls.Swap(0); n != 6 {
		t.Errorf("lore sync made %d network calls, want 6 (a fetch and an ls-remote per store)", n)
	}
}
