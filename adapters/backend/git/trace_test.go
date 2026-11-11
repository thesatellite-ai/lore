package git

// --trace (gitref.WithTracer, issue 15): a store clone prints its network
// calls only (fetch, ls-remote, push), each naming the host it reached:
// the setup's ssh alias for an owned store, the plain host otherwise.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/store"
)

// traceFakeSSH serves the "<owner>/<repo>.git" path of any host from
// $FAKE_SSH_ROOT, so a git@<host>: url reaches a local bare repo.
const traceFakeSSH = `#!/bin/sh
for a; do cmd=$a; done
cd "$FAKE_SSH_ROOT" && exec sh -c "$cmd"
`

func TestCloneTraceNamesTheHostReached(t *testing.T) {
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
			remote := buildRemote(t, remoteShape{head: "main", wantHead: "main"})
			if err := os.MkdirAll(filepath.Join(root, "acme"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(remote, filepath.Join(root, "acme", "hub.git")); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(t.TempDir(), "ssh")
			if err := os.WriteFile(bin, []byte(traceFakeSSH), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAKE_SSH_ROOT", root)
			route := WithRoute(func(context.Context, string) ([]string, []string) {
				return c.routeArgs, []string{"GIT_SSH_COMMAND=" + filepath.ToSlash(bin)}
			})
			var lines []string
			ctx := gitref.WithTracer(context.Background(), func(tc gitref.TraceCall) { lines = append(lines, gitref.TraceLine(tc)) })
			if _, err := OpenClone(ctx, "git@github.com:acme/hub.git", filepath.Join(t.TempDir(), "c"), rowfs.FolderRoot, store.OpenOptions{}, route); err != nil {
				t.Fatalf("open: %v", err)
			}
			if len(lines) == 0 {
				t.Fatal("the first open fetched but traced nothing")
			}
			for _, l := range lines {
				if sub := subcommand(strings.Fields(strings.TrimPrefix(l, "trace: git "))); !tracedSubcommands[sub] {
					t.Errorf("a local command was traced: %q", l)
				}
				if !strings.HasSuffix(l, ", remote "+c.wantHost+", env GIT_SSH_COMMAND)") {
					t.Errorf("line %q does not name remote %s", l, c.wantHost)
				}
			}
		})
	}
}
