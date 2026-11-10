package git

// openfetch_test.go — the first open of a store clone goes to the remote
// once (openFetch: every branch, every live ref and the remote HEAD in one
// fetch), and inside a FetchScope that fetch stands in for later ones.
// The remote is a local bare repository; a logging git wrapper (WithGit)
// counts the calls that reach the remote (fetch, ls-remote, push).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// networkSubcommands are the git subcommands that reach the remote.
var networkSubcommands = []string{"fetch", "ls-remote", "push"}

// loggingGit returns the options that make a store clone run git through the
// shared logging wrapper (loggingGitBin, written by TestMain in main_test.go), logging each call's arguments to the returned file
// (this test's own, under t.TempDir()). The log path rides on WithRoute's
// environment, which the backend adds to every git call of the clone.
func loggingGit(t *testing.T) (opts []Option, log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "git.log")
	env := []string{gitLogEnv + "=" + log}
	route := func(context.Context, string) (args, routeEnv []string) { return nil, env }
	return []Option{WithGit(loggingGitBin), WithRoute(route)}, log
}

// networkCalls lists the logged calls whose subcommand reaches the remote.
func networkCalls(t *testing.T, log string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		for _, sub := range networkSubcommands {
			if subcommand(strings.Fields(line)) == sub {
				out = append(out, line)
			}
		}
	}
	return out
}

// remoteShape builds one test remote for the first-open matrix.
type remoteShape struct {
	head     string // the remote's default branch ("" = an empty remote)
	others   []string
	marker   string // lore-store.yaml body ("" = the current marker)
	liveRef  bool   // push refs/lore/live/<head>
	wantHead string // the branch the clone must check out
	wantLive bool   // the live tracking ref survives the open
}

// buildRemote makes the bare remote of s.
func buildRemote(t *testing.T, s remoteShape) string {
	t.Helper()
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	initBranch := s.head
	if initBranch == "" {
		initBranch = "trunk"
	}
	gitT(t, dir, "init", "--quiet", "--bare", "--initial-branch="+initBranch, remote)
	if s.head == "" {
		return remote
	}
	seed := filepath.Join(dir, "seed")
	gitT(t, dir, "init", "--quiet", "--initial-branch="+s.head, seed)
	body := []byte(s.marker)
	if s.marker == "" {
		m, err := markerBody()
		if err != nil {
			t.Fatal(err)
		}
		body = m
	}
	if err := os.WriteFile(filepath.Join(seed, rowfs.MarkerFile), body, 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, seed, "add", rowfs.MarkerFile)
	gitT(t, seed, "commit", "--quiet", "-m", "init")
	gitT(t, seed, "push", "--quiet", remote, s.head)
	for _, o := range s.others {
		gitT(t, seed, "push", "--quiet", remote, "HEAD:refs/heads/"+o)
	}
	if s.liveRef {
		gitT(t, seed, "push", "--quiet", remote, "HEAD:refs/lore/live/"+s.head)
	}
	return remote
}

// markerBody is a current-version marker.
func markerBody() ([]byte, error) {
	id, err := ids.New[ids.Store]()
	if err != nil {
		return nil, err
	}
	return rowfs.Marker{ID: id}.Encode()
}

// v0117Marker is lore-store.yaml as v0.1.17 wrote it (version 1, no id).
const v0117Marker = "lore_store: 1\n"

func TestFirstOpenFetchesOnce(t *testing.T) {
	t.Parallel()
	cases := map[string]remoteShape{
		"main":                     {head: "main", wantHead: "main"},
		"non-main default":         {head: "trunk", others: []string{"main", "dev"}, wantHead: "trunk"},
		"empty store":              {wantHead: DefaultBranch},
		"v0117 store with live":    {head: "main", marker: v0117Marker, liveRef: true, wantHead: "main", wantLive: true},
		"v0117 store, trunk, live": {head: "trunk", others: []string{"main"}, marker: v0117Marker, liveRef: true, wantHead: "trunk", wantLive: true},
		"current store with live":  {head: "main", liveRef: true, wantHead: "main", wantLive: false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			remote := buildRemote(t, c)
			opts, log := loggingGit(t)
			b := clone(t, remote, opts...)
			calls := networkCalls(t, log)
			if len(calls) != 1 || subcommand(strings.Fields(calls[0])) != "fetch" {
				t.Fatalf("first open reached the remote %d times, want one fetch:\n%s", len(calls), strings.Join(calls, "\n"))
			}
			if b.Branch() != c.wantHead {
				t.Errorf("branch = %q, want %q", b.Branch(), c.wantHead)
			}
			if _, err := tryGit(t, b.Dir(), "rev-parse", "--verify", "--quiet", remoteHeadRef); err == nil {
				t.Errorf("%s was left behind", remoteHeadRef)
			}
			live, _ := tryGit(t, b.Dir(), "rev-parse", "--verify", "--quiet", LiveTrackingPrefix+c.wantHead)
			if gotLive := live != ""; gotLive != c.wantLive {
				t.Errorf("live tracking ref present = %v, want %v", gotLive, c.wantLive)
			}
		})
	}
}

func TestFetchScopeReusesTheOpenFetch(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		scoped    bool
		pushFirst bool // write and sync (push) before the counted sync
		wantSync  int  // network calls of the counted Sync
	}{
		"in scope":              {scoped: true, wantSync: 0},
		"no scope":              {scoped: false, wantSync: 1},
		"in scope after a push": {scoped: true, pushFirst: true, wantSync: 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			remote := newRemote(t, true)
			opts, log := loggingGit(t)
			ctx := context.Background()
			if c.scoped {
				ctx = WithFetchScope(ctx)
			}
			b, err := OpenClone(ctx, remote, filepath.Join(t.TempDir(), "clones", "host", "store"), rowfs.FolderRoot, store.OpenOptions{}, opts...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := b.Close(); err != nil {
					t.Error(err)
				}
			})
			if b.OpenFetchFresh(ctx) != c.scoped {
				t.Errorf("OpenFetchFresh = %v, want %v", b.OpenFetchFresh(ctx), c.scoped)
			}
			if b.OpenFetchFresh(WithFetchScope(context.Background())) {
				t.Error("OpenFetchFresh is true for another scope")
			}
			if c.pushFirst {
				g := newGen(t)
				write(t, b, store.Change{Row: g.row(newID(t), "a", hlc.HLC{})})
				if _, err := b.Sync(ctx); err != nil {
					t.Fatal(err)
				}
				if b.OpenFetchFresh(ctx) {
					t.Error("OpenFetchFresh is still true after a push")
				}
			}
			before := len(networkCalls(t, log))
			if _, err := b.Sync(ctx); err != nil {
				t.Fatal(err)
			}
			calls := networkCalls(t, log)[before:]
			if len(calls) != c.wantSync {
				t.Errorf("Sync reached the remote %d times, want %d:\n%s", len(calls), c.wantSync, strings.Join(calls, "\n"))
			}
		})
	}
}
