package wire

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/execfile"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestMigrateTeamLockRemoteGone (owner Mac run 2, item 34): a repo whose
// remote does not exist (a deleted repo, or one that was never ours: git
// says "Repository not found", or a path with no repository behind it)
// takes no team lock under `lore migrate run` or lore fix; the line says
// the remote does not exist and the run goes on locally. A remote that
// exists but cannot be reached is still E_OFFLINE.
//
// The fake ssh goes in GIT_SSH_COMMAND, not core.sshCommand: git lets
// that variable override the config, and the gate exports one
// (tests/lib/after-land.sh), so a config-only fake ran the real ssh there.
// t.Setenv needs the test and its subtests to run serially.
func TestMigrateTeamLockRemoteGone(t *testing.T) {
	bin := t.TempDir()
	fakeSSH := func(name, stderr string) string {
		p := filepath.Join(bin, name)
		body := "#!/bin/sh\nprintf '" + stderr + "' >&2\nexit 128\n"
		if err := execfile.WriteFile(p, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	notFound := fakeSSH("ssh-notfound", `ERROR: Repository not found.\n`)
	refused := fakeSSH("ssh-refused", `ssh: connect to host example.invalid port 22: Connection refused\n`)
	const sshURL = "ssh://git@example.invalid/acme/gone.git"
	cases := []struct {
		name, url, sshCmd string
		missing           bool
	}{
		{"missing path", filepath.Join(t.TempDir(), "no-such-repo.git"), "", true},
		{"repository not found", sshURL, notFound, true},
		{"unreachable", sshURL, refused, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := gitRepo(t)
			gitIn(t, repo, nil, "remote", "add", "origin", tc.url)
			if tc.sshCmd != "" {
				t.Setenv("GIT_SSH_COMMAND", tc.sshCmd)
			}
			// a person (or a coding agent acting for them): coordination needs one
			a, err := New(Options{RepoDir: repo, Home: srcHome(t, "identity:\n  key: human:pat@example.com\n  name: Pat\n")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			withProject(t, a)
			ctx := context.Background()
			for _, lock := range []struct {
				name string
				acq  func(context.Context) (func() error, string, error)
			}{{"migrate run", a.MigrateTeamLock().Acquire}, {"lore fix", a.fixTeamLock().Acquire}} {
				release, line, err := lock.acq(ctx)
				if !tc.missing {
					if !errs.Has(err, errs.Offline) {
						t.Fatalf("%s: an unreachable remote = %q, %v; want E_OFFLINE", lock.name, line, err)
					}
					continue
				}
				if err != nil || release != nil {
					t.Fatalf("%s: a remote that does not exist = release %v, %v; want no lock and no error", lock.name, release != nil, err)
				}
				if !strings.Contains(line, "team lock: skipped, the remote origin does not exist") || !strings.Contains(line, "migrating locally") {
					t.Fatalf("%s: line %q does not say the remote does not exist and the run goes on locally", lock.name, line)
				}
			}
		})
	}
}
