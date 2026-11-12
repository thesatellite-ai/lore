package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// TestNetworkSwitchesReachNoRemote pins owner bug 14 at the store clone:
// with LORE_OFFLINE=1 RefreshClone (and any fetch through this package's
// runner) never starts the remote side, with LORE_NO_FETCH=1 neither does
// a fetch, and without them the same fetch does reach it (the marker
// proves the remote is wired). The remote's upload-pack is a script that
// leaves a marker file when run.
func TestNetworkSwitchesReachNoRemote(t *testing.T) {
	bin, err := exec.LookPath(DefaultBinary)
	if err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "touched")
	script := filepath.Join(dir, "upload-pack")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nexec git-upload-pack \"$@\"\n"), 0o700); err != nil { // #nosec G306 -- an executable test script
		t.Fatal(err)
	}
	gitRun := func(args ...string) {
		t.Helper()
		cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, args...), Dir: dir, Env: append(gitenv.Clean(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	gitRun("init", "-q", "-b", "main", "src")
	gitRun("-C", "src", "commit", "-q", "--allow-empty", "-m", "one")
	gitRun("clone", "-q", "--bare", "src", "remote.git")
	gitRun("init", "-q", "clone")
	gitRun("-C", "clone", "remote", "add", RemoteName, filepath.Join(dir, "remote.git"))
	gitRun("-C", "clone", "config", "remote."+RemoteName+".uploadpack", script)
	clone := filepath.Join(dir, "clone")
	ctx := context.Background()

	for _, sw := range []string{gitenv.EnvOffline, gitenv.EnvNoFetch} {
		t.Run(sw, func(t *testing.T) {
			t.Setenv(sw, gitenv.On)
			if err := RefreshClone(ctx, clone); err == nil {
				t.Fatal("RefreshClone under " + sw + " answered as fetched")
			}
			b := &Backend{git: runner{bin: bin}}
			if err := b.runFetch(ctx, clone, "fetch", "--quiet", RemoteName); err != nil {
				t.Fatalf("a store fetch under %s fails instead of reading what is on disk: %v", sw, err)
			}
			if _, serr := os.Stat(marker); serr == nil {
				t.Fatal("the remote was reached under " + sw)
			}
		})
	}
	if err := RefreshClone(ctx, clone); err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(marker); serr != nil {
		t.Fatal("the online fetch left no marker: the test remote is not wired")
	}
}
