package host

// devco:D-1026 (H-290): the first read of a session made two network round
// trips one after the other (the org's link index fetch inside OrgLive,
// then the repo's live fetch). A read's live fetch now runs while OrgLive
// resolves: these tests hold OrgLive open until the repo's fetch landed
// (old code: it never does, the fetch only started after OrgLive).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// prefetchWait bounds how long OrgLive waits for the early fetch: far
// above a local fetch, so a slow test machine never fails the old-code
// side wrongly, and the old code fails by this timeout.
const prefetchWait = 5 * time.Second

// prefetchRepo is a repo whose origin (a local bare repo) holds main and
// a live ref the clone has not fetched yet; it returns the repo folder.
func prefetchRepo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	testenv.SetHome(t, home)
	t.Setenv(EnvLive, "")
	t.Setenv(EnvSync, "")
	t.Setenv(EnvLiveAutopush, "")
	t.Setenv(EnvLiveRemote, "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	if err := os.MkdirAll(filepath.Join(dir, ".lore", "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(t.TempDir(), "acme.git")
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", bare)
	gitT(t, dir, "push", "-q", bare, "main:main", "main:"+live.RefLive)
	gitT(t, dir, "remote", "add", "origin", bare)
	return dir
}

// hasRef reports whether the repo at dir holds ref.
func hasRef(dir, ref string) bool {
	_, err := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: dir, Args: []string{"rev-parse", "--verify", "--quiet", ref}})
	return err == nil
}

// waitRef polls until dir holds ref or prefetchWait passes.
func waitRef(dir, ref string) bool {
	for end := time.Now().Add(prefetchWait); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if hasRef(dir, ref) {
			return true
		}
	}
	return false
}

// prefetchSession opens the session of dir for one run (read or write)
// with orgLive as the org's live settings, and finishes the run at the
// end of the test.
func prefetchSession(t *testing.T, dir string, read bool, orgLive func(context.Context) *orgconf.Live) *liveSession {
	t.Helper()
	h := &liveHost{cfg: LiveConfig{Home: filepath.Join(os.Getenv("HOME"), ".lore"), WorkDir: dir, Version: "0.1.18", Stderr: &strings.Builder{}, OrgLive: orgLive}}
	r := &liveRun{h: h, sessions: map[string]*liveSession{}, read: read}
	ctx := withRun(context.Background(), r)
	s := r.session(ctx, dir)
	if s == nil {
		t.Fatal("live mode must apply in a git repo with LORE_LIVE unset")
	}
	t.Cleanup(func() { r.finish(ctx) })
	return s
}

// tracking is where a fetch of origin puts the remote's live ref.
var tracking = gitTrackingLive("origin")

// gitTrackingLive is the tracking ref of remote's live ref.
func gitTrackingLive(remote string) string {
	return strings.Replace(live.RefLive, "refs/lore/", "refs/lore-remote/"+remote+"/", 1)
}

// TestReadLiveFetchOverlapsOrgLookup: a read's repo fetch lands while the
// org's live settings still resolve, and the session takes it as the
// run's one fetch (stamped, no second fetch).
func TestReadLiveFetchOverlapsOrgLookup(t *testing.T) {
	dir := prefetchRepo(t)
	seen := false
	s := prefetchSession(t, dir, true, func(context.Context) *orgconf.Live {
		seen = waitRef(dir, tracking)
		return nil
	})
	if !seen {
		t.Fatalf("the read's live fetch did not land while the org's settings resolved (%s missing): the two fetches run one after the other", tracking)
	}
	if !s.fetched {
		t.Fatal("the session must take the early fetch as this run's fetch")
	}
	if live.FetchStamp(s.stateDir).IsZero() {
		t.Fatal("the early fetch must be stamped like any fetch of the run")
	}
	if s.lock == nil {
		t.Fatal("the session must hold the live lock the early fetch took")
	}
}

// TestWriteLiveFetchWaitsForOrgSettings: a write decides its fetch with the
// org's settings (its write TTL, the tracking refspec first), so nothing
// is fetched before they resolve; it fetches in prepare as before.
func TestWriteLiveFetchWaitsForOrgSettings(t *testing.T) {
	dir := prefetchRepo(t)
	early := true
	s := prefetchSession(t, dir, false, func(context.Context) *orgconf.Live {
		time.Sleep(200 * time.Millisecond) // time for a wrongly started fetch to land
		early = hasRef(dir, tracking)
		return nil
	})
	if early {
		t.Fatal("a write must not fetch before the org's live settings resolve")
	}
	if !s.fetched || !hasRef(dir, tracking) {
		t.Fatal("the write must still fetch in prepare")
	}
}

// TestReadOrgOtherRemoteFetchesItsOwn: an org that names another live
// remote gets that remote fetched by prepare; the early fetch of the home
// config's remote is not taken as the run's fetch.
func TestReadOrgOtherRemoteFetchesItsOwn(t *testing.T) {
	dir := prefetchRepo(t)
	bare2 := filepath.Join(t.TempDir(), "widgets.git")
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", bare2)
	gitT(t, dir, "push", "-q", bare2, "main:main", "main:"+live.RefLive)
	gitT(t, dir, "remote", "add", "team", bare2)
	s := prefetchSession(t, dir, true, func(context.Context) *orgconf.Live {
		waitRef(dir, tracking) // let the early fetch of origin land first
		return &orgconf.Live{Remote: "team"}
	})
	if s.ref.RemoteName != "team" {
		t.Fatalf("the org's remote must win: session remote %q", s.ref.RemoteName)
	}
	if !hasRef(dir, gitTrackingLive("team")) || !s.fetched {
		t.Fatal("prepare must fetch the org's remote itself")
	}
}
