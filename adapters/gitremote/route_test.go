package gitremote_test

// Git's RouteArgs and RouteEnv reach every network call (issue 15): a
// fake ssh (passed through RouteEnv as GIT_SSH_COMMAND) logs the host git
// connects to and serves a local bare repo, so List, Fetch and Push all
// run and the log shows the aliased host on each.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/logic/gitenv"
)

const fakeSSH = `#!/bin/sh
for a; do host=$cmd; cmd=$a; done
echo "$host ${cmd%% *}" >> "$FAKE_SSH_LOG"
cd "$FAKE_SSH_ROOT" && exec sh -c "$cmd"
`

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitenv.Clean(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRouteReachesEveryNetworkCall(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "acme", "hub.git")
	run(t, root, "init", "-q", "--bare", "-b", "main", origin)
	local := t.TempDir()
	run(t, local, "init", "-q", "-b", "main")
	run(t, local, "commit", "-q", "--allow-empty", "-m", "one")
	run(t, local, "remote", "add", "origin", "git@github.com:acme/hub.git")
	bin := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(bin, []byte(fakeSSH), 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_ROOT", root)
	t.Setenv("FAKE_SSH_LOG", log)
	repo, err := gitref.Open(context.Background(), local, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := &gitremote.Git{Repo: repo, Name: "origin",
		RouteArgs: []string{"-c", "url.git@github-work:.insteadOf=git@github.com:"},
		RouteEnv:  []string{"GIT_SSH_COMMAND=" + filepath.ToSlash(bin)}}
	ctx := context.Background()
	head := run(t, local, "rev-parse", "HEAD")
	if _, err := g.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: head, Dst: "refs/heads/main", Mode: gitremote.ModeFastForward}}}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if _, err := g.List(ctx); err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := g.Fetch(ctx, gitremote.FetchRequest{Refspecs: gitremote.TrackingRefspecs("origin", "main")}); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "git@github-work git-receive-pack\ngit@github-work git-upload-pack\ngit@github-work git-upload-pack"
	if got := strings.TrimSpace(string(b)); got != want {
		t.Errorf("ssh calls\n got %q\nwant %q", got, want)
	}
}

// --trace (gitref.WithTracer): List, Fetch and Push each print one line
// naming the host reached: the alias for an owned store's route, the plain
// host for a store no setup owns (issue 15). The repo's own runner sees the
// calls marked traced, so a tracing runner does not print them twice.
func TestTraceNamesTheHostReached(t *testing.T) {
	cases := []struct {
		name      string
		routeArgs []string
		wantHost  string
	}{
		{"owned store: the setup's alias", []string{"-c", "url.git@github-work:.insteadOf=git@github.com:"}, "git@github-work"},
		{"unowned store: the plain host", nil, "git@github.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			run(t, root, "init", "-q", "--bare", "-b", "main", filepath.Join(root, "acme", "hub.git"))
			local := t.TempDir()
			run(t, local, "init", "-q", "-b", "main")
			run(t, local, "commit", "-q", "--allow-empty", "-m", "one")
			run(t, local, "remote", "add", "origin", "git@github.com:acme/hub.git")
			bin := filepath.Join(t.TempDir(), "ssh")
			if err := os.WriteFile(bin, []byte(fakeSSH), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAKE_SSH_ROOT", root)
			t.Setenv("FAKE_SSH_LOG", filepath.Join(t.TempDir(), "ssh.log"))
			inner := &markRecorder{}
			repo, err := gitref.Open(context.Background(), local, inner)
			if err != nil {
				t.Fatal(err)
			}
			inner.opened = true
			g := &gitremote.Git{Repo: repo, Name: "origin", RouteArgs: c.routeArgs, RouteEnv: []string{"GIT_SSH_COMMAND=" + filepath.ToSlash(bin)}}
			var lines []string
			ctx := gitref.WithTracer(context.Background(), func(tc gitref.TraceCall) { lines = append(lines, gitref.TraceLine(tc)) })
			head := run(t, local, "rev-parse", "HEAD")
			if _, err := g.Push(ctx, gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: head, Dst: "refs/heads/main", Mode: gitremote.ModeFastForward}}}); err != nil {
				t.Fatalf("push: %v", err)
			}
			if _, err := g.List(ctx); err != nil {
				t.Fatalf("list: %v", err)
			}
			if err := g.Fetch(ctx, gitremote.FetchRequest{Refspecs: gitremote.TrackingRefspecs("origin", "main")}); err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if len(lines) != 3 {
				t.Fatalf("trace lines %q, want 3", lines)
			}
			for i, sub := range []string{" push ", " ls-remote ", " fetch "} {
				if !strings.Contains(lines[i], sub) || !strings.HasSuffix(lines[i], ", remote "+c.wantHost+", env GIT_SSH_COMMAND)") {
					t.Errorf("line %d = %q, want a%sline naming remote %s", i, lines[i], sub, c.wantHost)
				}
			}
			if inner.unmarked != 0 {
				t.Errorf("%d network calls reached the repo's runner unmarked (a tracing runner would print them twice)", inner.unmarked)
			}
		})
	}
}

// markRecorder is gitref.Exec counting calls whose context is not marked
// traced (gitref.MarkTraced) after Open.
type markRecorder struct {
	opened   bool
	unmarked int
}

func (m *markRecorder) Run(ctx context.Context, inv gitref.Invocation) ([]byte, error) {
	if m.opened && !gitref.IsMarkedTraced(ctx) {
		m.unmarked++
	}
	return gitref.Exec{}.Run(ctx, inv)
}
