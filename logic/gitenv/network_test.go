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

// TestStoreNetRefused (owner item 21): LORE_NO_STORE_FETCH, the switch
// lore fix --all gives its children, refuses a fetch only through
// StoreNetRefused (the store clone paths); NetRefused, which every other
// runner (the repo's own remote, the signals ref of the team lock) uses,
// lets the same fetch run.
func TestStoreNetRefused(t *testing.T) {
	nsf := []string{EnvNoStoreFetch + "=1"}
	if why, refused := NetRefused(nsf, []string{"fetch", "origin"}); refused {
		t.Fatalf("NetRefused under %s refused the repo's own fetch: %s", EnvNoStoreFetch, why)
	}
	if NoFetch(nsf) {
		t.Fatalf("NoFetch is on under %s alone", EnvNoStoreFetch)
	}
	for _, c := range []struct {
		env  []string
		args []string
		want bool
	}{
		{nsf, []string{"fetch", "origin"}, true},
		{nsf, []string{"-C", "/s", "ls-remote", "origin"}, true},
		{nsf, []string{"push", "origin"}, false},
		{nsf, []string{"ls-tree", "HEAD"}, false},
		{[]string{EnvNoFetch + "=1"}, []string{"fetch"}, true},
		{[]string{EnvOffline + "=1"}, []string{"push"}, true},
		{nil, []string{"fetch"}, false},
	} {
		why, got := StoreNetRefused(c.env, c.args)
		if got != c.want || (got && why == "") {
			t.Errorf("StoreNetRefused(%v, %v) = %q, %v; want %v", c.env, c.args, why, got, c.want)
		}
	}
	if !NoStoreFetch(nsf) || !NoStoreFetch([]string{EnvOffline + "=1"}) || NoStoreFetch(nil) {
		t.Fatal("NoStoreFetch does not follow its switches")
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
