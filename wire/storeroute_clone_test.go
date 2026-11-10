package wire

// The store clone backend's route end to end (onboard #15 follow-up): a
// clone, fetch and push of a store a setup owns go through that setup's
// ssh alias; a store no setup owns goes to the plain host even while a
// setup with an alias is active. The host is read from the --trace line
// (gitref.TraceLine), which names the host each network call reached.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire/internal/fakebin"
)

// cloneRouteConfig: personal.store is shared by the inheriting setup
// `home` (alias github-home); `work` owns acme's org store (alias
// github-work); `solo` (alias github-solo) is the active setup in every
// case and owns only its own personal store.
const cloneRouteConfig = `personal:
  store: git@github.com:alice/lore-me.git
orgs:
  - name: acme
    stores:
      - url: git@github.com:acme/hub.git
setups:
  - name: work
    kind: member
    org: acme
    personal_store: none
    github: {user: alice-work, host_alias: github-work}
  - name: home
    kind: solo
    personal_store: inherit
    github: {user: alice, host_alias: github-home}
  - name: solo
    kind: solo
    personal_store: git@github.com:alice-solo/notes.git
    github: {user: alice-solo, host_alias: github-solo}
`

// cloneFakeSSH serves "<owner>/<repo>.git" of any host from $FAKE_SSH_ROOT.
const cloneFakeSSH = "#!/bin/sh\nfor a; do cmd=$a; done\ncd \"$FAKE_SSH_ROOT\" && exec sh -c \"$cmd\"\n"

// storeMemories is the table the test row is written to.
const storeMemories row.Table = "memories"

func TestCloneRouteOwnedInheritUnowned(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(cloneRouteConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LORE_SETUP", "solo")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR", "GIT_COMMITTER"} {
		t.Setenv(k+"_NAME", "Alice")
		t.Setenv(k+"_EMAIL", "alice@example.com")
	}
	sshBin := filepath.Join(t.TempDir(), "ssh")
	fakebin.Write(t, sshBin, cloneFakeSSH, 0o755)
	t.Setenv("GIT_SSH_COMMAND", sshBin)

	cases := []struct {
		name, url, wantHost string
	}{
		{"owned: an org store of the work setup", "git@github.com:acme/hub.git", "git@github-work"},
		{"inherit: personal.store of the inheriting home setup", "git@github.com:alice/lore-me.git", "git@github-home"},
		{"unowned: not the active solo setup's alias", "git@github.com:acme/site.git", "git@github.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			owner, repo, _ := strings.Cut(strings.TrimPrefix(c.url, "git@github.com:"), "/")
			bare := filepath.Join(root, owner, repo)
			seedStore(t, bare)
			t.Setenv("FAKE_SSH_ROOT", root)
			route := git.WithRoute(func(ctx context.Context, remote string) ([]string, []string) {
				return cloneRoute(ctx, home, t.TempDir(), remote)
			})
			// phase is the step running; seen is "<phase> <subcommand>" per
			// network call (the first open clones by init + fetch; a sync
			// fetches, then pushes the written row).
			phase := "clone"
			seen := map[string]bool{}
			ctx := gitref.WithTracer(context.Background(), func(tc gitref.TraceCall) {
				l := gitref.TraceLine(tc)
				for _, sub := range []string{"fetch", "push"} {
					if strings.Contains(l, " "+sub+" ") {
						seen[phase+" "+sub] = true
					}
				}
				if !strings.Contains(l, ", remote "+c.wantHost) {
					t.Errorf("%s reached the wrong host: %q, want remote %s", c.url, l, c.wantHost)
				}
			})
			dir := filepath.Join(t.TempDir(), "c")
			b, err := git.OpenClone(ctx, c.url, dir, rowfs.FolderRoot, store.OpenOptions{}, route)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			phase = "sync"
			if err := b.Write(ctx, []store.Change{{Row: cloneTestRow(t)}}); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, err := b.Sync(ctx); err != nil {
				t.Fatalf("sync: %v", err)
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"clone fetch", "sync fetch", "sync push"} {
				if !seen[want] {
					t.Errorf("%s: no %q network call traced (saw %v)", c.url, want, seen)
				}
			}
		})
	}
}

// cloneTestRow is one memory row.
func cloneTestRow(t *testing.T) store.Row {
	t.Helper()
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	v, err := value.TextOf("route test")
	if err != nil {
		t.Fatal(err)
	}
	stamp := hlc.NewClock(actor, time.Now).Now(hlc.HLC{})
	r, err := store.FromRow(row.Row{Table: storeMemories, ID: id.Any(), HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor, Fields: map[string]value.Value{"body": v}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// seedStore makes bare a lore store remote: one commit holding a marker.
func seedStore(t *testing.T, bare string) {
	t.Helper()
	seed := t.TempDir()
	gitIn := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	gitIn(seed, "init", "--quiet", "--bare", "--initial-branch=main", bare)
	gitIn(seed, "init", "--quiet", "--initial-branch=main", ".")
	id, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	body, err := rowfs.Marker{ID: id}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, rowfs.MarkerFile), body, 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(seed, "add", rowfs.MarkerFile)
	gitIn(seed, "commit", "--quiet", "-m", "init")
	gitIn(seed, "push", "--quiet", bare, "main")
}
