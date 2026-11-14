package gitremote_test

// retry_test.go — a call whose connection never opened is tried once more
// (devco L-10, L-12: one dropped ssh call failed a whole claim or sync),
// while a remote that answers with a refusal is not retried.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitremote"
	gt "github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
)

// flakySSH writes a fake ssh that counts its calls in dir/count and fails
// the calls failAt says (1-based; 0 means every call) with msg, else runs
// git's command locally, and returns the env that routes git through it.
func flakySSH(t *testing.T, dir string, failAt int, msg string) []string {
	t.Helper()
	script := filepath.Join(dir, "ssh.sh")
	// git's "ssh -G" probe of the ssh variant is not a call to the remote
	body := `#!/bin/sh
[ "$1" = "-G" ] && exit 0
c="` + dir + `/count"
n=$(( $(cat "$c" 2>/dev/null || echo 0) + 1 )); echo $n > "$c"
for last; do :; done
if [ ` + strconv.Itoa(failAt) + ` -eq 0 ] || [ $n -eq ` + strconv.Itoa(failAt) + ` ]; then echo "` + msg + `" >&2; exit 255; fi
exec sh -c "$last"
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return []string{"GIT_SSH_COMMAND=" + script}
}

func sshCalls(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "count"))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

// sshClone is a clone of a new world whose origin is reached through the
// fake ssh at ssh://fakehost<origin>.
func sshClone(t *testing.T, failAt int, msg string) (*gt.Clone, string) {
	t.Helper()
	w := gt.NewWorld(t)
	c := w.Clone("a")
	gt.Git(t, c.Dir, "remote", "set-url", gt.RemoteName, "ssh://fakehost"+w.OriginDir)
	dir := t.TempDir()
	c.Remote = &gitremote.Git{Repo: c.Repo, Name: gt.RemoteName, RouteEnv: flakySSH(t, dir, failAt, msg)}
	return c, dir
}

const timedOut = "ssh: connect to host fakehost port 22: Connection timed out"

func TestDroppedConnectionIsRetriedOnce(t *testing.T) {
	c, dir := sshClone(t, 1, timedOut)
	c.Fetch() // fails the test if the retry did not run
	if n := sshCalls(t, dir); n != 2 {
		t.Fatalf("fetch made %d ssh calls, want 2 (one dropped, one retry)", n)
	}
	c.Write(signals, "claims/t1.json", "v1")
	c.MustPush(signals) // call 3 connects at once
	if n := sshCalls(t, dir); n != 3 {
		t.Fatalf("after the push %d ssh calls, want 3", n)
	}
}

func TestDroppedPushIsRetriedOnceAndApplied(t *testing.T) {
	c, dir := sshClone(t, 1, timedOut)
	c.Write(signals, "claims/t1.json", "v1")
	c.MustPush(signals)
	if n := sshCalls(t, dir); n != 2 {
		t.Fatalf("push made %d ssh calls, want 2", n)
	}
}

func TestDownRemoteFailsAfterOneRetryWithGitsWords(t *testing.T) {
	c, dir := sshClone(t, 0, timedOut)
	err := c.Remote.Fetch(context.Background(), gitremote.FetchRequest{Refspecs: gitremote.TrackingRefspecs(gt.RemoteName, gt.DefaultBranch)})
	var te *gitremote.TransportError
	if !errors.As(err, &te) || !errors.Is(err, gitremote.ErrUnconfirmed) || !strings.Contains(te.Stderr, "Connection timed out") {
		t.Fatalf("got %v, want ErrUnconfirmed naming the timeout", err)
	}
	if n := sshCalls(t, dir); n != 2 {
		t.Fatalf("a down remote got %d ssh calls, want 2 (one retry, no more)", n)
	}
}

func TestRefusalIsNotRetried(t *testing.T) {
	c, dir := sshClone(t, 0, "git@fakehost: Permission denied (publickey).")
	err := c.Remote.Fetch(context.Background(), gitremote.FetchRequest{Refspecs: gitremote.TrackingRefspecs(gt.RemoteName, gt.DefaultBranch)})
	if err == nil {
		t.Fatal("fetch passed through a refusing ssh")
	}
	if n := sshCalls(t, dir); n != 1 {
		t.Fatalf("a refusal got %d ssh calls, want 1 (a retry cannot change the answer)", n)
	}
}
