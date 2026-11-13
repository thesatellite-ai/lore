package orgcfg

// Every network call this package makes for a store (fetch, ls-remote,
// push) goes through the Router (issue 15): a fake ssh logs the host git
// connects to and serves a local bare repo, so the calls succeed and the
// log shows which account's host each one used.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// fakeSSH is a GIT_SSH_COMMAND that appends "<host> <git command>" to log
// and runs the command in root (where the "owner/repo.git" path lives).
const fakeSSH = `#!/bin/sh
for a; do host=$cmd; cmd=$a; done
echo "$host ${cmd%% *}" >> "$FAKE_SSH_LOG"
cd "$FAKE_SSH_ROOT" && exec sh -c "$cmd"
`

// sshWorld serves origin as git@github.com:acme/hub.git through the fake
// ssh; it returns that url and the log path.
func sshWorld(t *testing.T, origin string) (url, log string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "acme"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(origin, filepath.Join(root, "acme", "hub.git")); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(bin, []byte(fakeSSH), 0o700); err != nil {
		t.Fatal(err)
	}
	log = filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("GIT_SSH_COMMAND", filepath.ToSlash(bin)) // git runs it through sh
	t.Setenv("FAKE_SSH_ROOT", root)
	t.Setenv("FAKE_SSH_LOG", log)
	return "git@github.com:acme/hub.git", log
}

// aliasRouter routes only owned through the alias github-work (the shape
// of wire's storeRouter: nothing for a url no setup owns).
func aliasRouter(owned string, asked *[]string) Router {
	return func(_ context.Context, u string) ([]string, []string) {
		*asked = append(*asked, u)
		if u != owned {
			return nil, nil
		}
		return []string{"-c", "url.git@github-work:.insteadOf=git@github.com:"}, nil
	}
}

func readLog(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestNetworkCallsUseTheStoreRoute(t *testing.T) {
	cases := []struct {
		name     string
		owned    string // the url the router has a setup for
		wantHost string
	}{
		{"a setup's store goes through its alias", "git@github.com:acme/hub.git", "git@github-work"},
		{"a store no setup owns stays on github.com", "git@github.com:acme/other.git", "git@github.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// --trace (gitref.WithTracer): every network call prints the host
			// it reached, the alias for an owned store (issue 15).
			var traced []string
			ctx := gitref.WithTracer(context.Background(), func(tc gitref.TraceCall) { traced = append(traced, gitref.TraceLine(tc)) })
			origin := storeOrigin(t)
			url, log := sshWorld(t, origin)
			var asked []string
			r := aliasRouter(c.owned, &asked)
			home := t.TempDir()
			cl := Clones{Home: home, Route: r}
			norm, nerr := repourl.Normalize(url) // the clone is named by the url
			if nerr != nil {
				t.Fatal(nerr)
			}
			// fetch + ls-remote (Doctor: ensureClone, fetch, recordFetch)
			info := cl.Doctor(ctx, norm, url, []string{"main"})
			if len(info.Problems) != 0 {
				t.Fatalf("doctor problems: %v", info.Problems)
			}
			// ls-remote --heads + push (commitStore, the link and org-file
			// write path: it asks the remote before its first push, L-2)
			sc := putCommit(cl.Dir(norm), func(int) {})
			sc.Route = r
			res, err := commitStore(ctx, sc)
			if err != nil || !res.Pushed {
				t.Fatalf("push: res=%+v err=%v", res, err)
			}
			want := []string{c.wantHost + " git-upload-pack", c.wantHost + " git-upload-pack", c.wantHost + " git-upload-pack", c.wantHost + " git-receive-pack"}
			got := readLog(t, log)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("ssh calls\n got %q\nwant %q", got, want)
			}
			for _, u := range asked {
				if u != url {
					t.Errorf("the router was asked for %q, want the clone's origin %q", u, url)
				}
			}
			if len(asked) != 4 {
				t.Errorf("router asked %d times, want once per network call (4)", len(asked))
			}
			if len(traced) != 4 {
				t.Fatalf("trace lines %q, want one per network call (4)", traced)
			}
			for i, sub := range []string{" fetch ", " ls-remote ", " ls-remote --heads ", " push "} {
				if !strings.Contains(traced[i], sub) || !strings.HasSuffix(traced[i], ", remote "+c.wantHost+")") {
					t.Errorf("trace line %d = %q, want a%sline naming remote %s", i, traced[i], sub, c.wantHost)
				}
			}
		})
	}
}
