package gitenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestNetRefused pins the two switches (owner bug 14): LORE_OFFLINE
// refuses every call that reaches a remote, LORE_NO_FETCH only the reads
// (a push still runs), local calls always run, and -C / -c values are not
// taken for the subcommand.
func TestNetRefused(t *testing.T) {
	off := []string{EnvOffline + "=1"}
	nof := []string{EnvNoFetch + "=1"}
	cases := []struct {
		env  []string
		args []string
		want bool
	}{
		{off, []string{"fetch", "origin"}, true},
		{off, []string{"-C", "/x", "push", "origin"}, true},
		{off, []string{"-c", "a=b", "clone", "u", "d"}, true},
		{off, []string{"ls-remote", "--heads"}, true},
		{off, []string{"-C", "fetch", "ls-tree", "HEAD"}, false},
		{nof, []string{"fetch", "origin"}, true},
		{nof, []string{"ls-remote"}, true},
		{nof, []string{"push", "origin"}, false},
		{nil, []string{"fetch", "origin"}, false},
		{[]string{EnvOffline + "=0"}, []string{"fetch"}, false},
	}
	for _, c := range cases {
		why, got := NetRefused(c.env, c.args)
		if got != c.want || (got && why == "") {
			t.Errorf("NetRefused(%v, %v) = %q, %v; want %v", c.env, c.args, why, got, c.want)
		}
	}
}

// TestRepoNetRefused: a code repo's own git calls (its live ref, the team
// lock migrate takes on refs/lore/signals) still fetch under
// LORE_NO_FETCH, which covers only the store clones lore fix --all
// fetched first; LORE_OFFLINE refuses them like any call.
func TestRepoNetRefused(t *testing.T) {
	nof := []string{EnvNoFetch + "=1"}
	off := []string{EnvOffline + "=1"}
	if why, got := RepoNetRefused(nof, []string{"fetch", "origin", "refs/lore/signals"}); got {
		t.Errorf("a repo fetch was refused under LORE_NO_FETCH: %q", why)
	}
	if _, got := RepoNetRefused(off, []string{"fetch", "origin"}); !got {
		t.Error("a repo fetch ran under LORE_OFFLINE")
	}
	if _, got := RepoNetRefused(off, []string{"push", "origin"}); !got {
		t.Error("a repo push ran under LORE_OFFLINE")
	}
}

// TestOfflineGitReachesNoRemote: under LORE_OFFLINE, Clean's environment
// makes git itself refuse a transport, so even a git lore starts without
// the runners' check never connects. The remote is a local bare repo with
// an upload-pack that leaves a marker: the marker never appears.
func TestOfflineGitReachesNoRemote(t *testing.T) {
	bin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "touched")
	script := filepath.Join(dir, "upload-pack")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nexec git-upload-pack \"$@\"\n"), 0o700); err != nil { // #nosec G306 -- an executable test script
		t.Fatal(err)
	}
	run := func(env []string, args ...string) ([]byte, error) {
		cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, args...), Dir: dir, Env: env}
		return cmd.CombinedOutput()
	}
	base := Clean(os.Environ())
	if out, err := run(base, "init", "-q", "--bare", "remote.git"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if out, err := run(base, "init", "-q", "clone"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	offline := Clean(append(os.Environ(), EnvOffline+"=1"))
	if !slices.Contains(offline, noTransport) {
		t.Fatalf("Clean under %s=1 does not set %s", EnvOffline, noTransport)
	}
	out, err := run(offline, "-C", "clone", "fetch", "--upload-pack="+script, filepath.Join(dir, "remote.git"))
	if err == nil || !strings.Contains(string(out), "not allowed") {
		t.Fatalf("offline fetch ran: %v %s", err, out)
	}
	if _, serr := os.Stat(marker); serr == nil {
		t.Fatal("offline fetch reached the remote")
	}
	_, _ = run(base, "-C", "clone", "fetch", "--upload-pack="+script, filepath.Join(dir, "remote.git")) // an empty remote: git connects, then finds no HEAD
	if _, serr := os.Stat(marker); serr != nil {
		t.Fatal("the marker script is not wired (the online fetch left no marker)")
	}
}
