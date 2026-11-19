package orgcfg

// devco D-714: an org-file commit (org link, unlink, org config, org store
// add/remove/audience, set-url) of an org with no identity signed
// "lore <lore@localhost>" while the git backend's commits in the same
// store carried the clone's git identity (FLAG4). commitStore now resolves
// a signature without a name or email the backend's way.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

func TestCommitStoreSignerFallsBackLikeTheBackend(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	when := time.Date(2026, time.November, 6, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		sig      gitref.Signature
		gitUser  [2]string // the clone's own user.name / user.email
		wantSign string
	}{
		{"org identity signs whatever git says", gitref.Signature{Name: "Ada Admin", Email: "ada@acme.example", When: when},
			[2]string{"ada-handle", "ada@home.example"}, "Ada Admin <ada@acme.example>"},
		{"no org identity: the clone's git identity", gitref.Signature{When: when},
			[2]string{"Bo Builder", "bo@acme.example"}, "Bo Builder <bo@acme.example>"},
		{"no org identity, no git identity: lore's last resort", gitref.Signature{When: when},
			[2]string{}, "lore <lore@localhost>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			origin, clone := pushFixture(t)
			if c.gitUser[0] != "" {
				testGit(t, clone, "config", "user.name", c.gitUser[0])
				testGit(t, clone, "config", "user.email", c.gitUser[1])
			}
			sc := putCommit(clone, func(int) {})
			sc.Signature = c.sig
			res, err := commitStore(context.Background(), sc)
			if err != nil || !res.Pushed {
				t.Fatalf("commitStore: res=%+v err=%v", res, err)
			}
			for _, f := range []string{"%an <%ae>", "%cn <%ce>"} {
				if got := testGit(t, origin, "log", "-1", "--format="+f, "main"); got != c.wantSign {
					t.Fatalf("%s: commit signed %q, want %q", f, got, c.wantSign)
				}
			}
			if got := testGit(t, origin, "log", "-1", "--format=%at", "main"); got != strconv.FormatInt(when.Unix(), 10) {
				t.Fatalf("commit time %q, want the caller's When", got)
			}
		})
	}
}
