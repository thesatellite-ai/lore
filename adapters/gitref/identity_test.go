package gitref

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
)

// FLAG4 (owner, r2-ident): GitIdentity is what git itself would sign with in
// this repo; ok only when both user.name and user.email are set and usable,
// so the live session falls back to the lore identity otherwise.
func TestGitIdentityMatrix(t *testing.T) {
	// No global/system git config may leak in: the matrix sets only the
	// repo's own config.
	testenv.SetHome(t, t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	cases := []struct {
		name        string
		user, email string // "" = unset
		want        Signature
		ok          bool
	}{
		{"both set", "Dev One", "dev1@example.test", Signature{Name: "Dev One", Email: "dev1@example.test"}, true},
		{"unicode name", "Zoë Ångström", "zoe@example.test", Signature{Name: "Zoë Ångström", Email: "zoe@example.test"}, true},
		{"name only", "Dev One", "", Signature{}, false},
		{"email only", "", "dev1@example.test", Signature{}, false},
		{"neither", "", "", Signature{}, false},
		{"blank name", "   ", "dev1@example.test", Signature{}, false},
		{"angle bracket in email", "Dev", "a<b>@example.test", Signature{}, false},
	}
	for _, kind := range []repoKind{kindNormal, kindBare} {
		for _, tc := range cases {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				r := newRepo(t, kind)
				if tc.user != "" {
					git(t, r.Dir(), "config", "user.name", tc.user)
				}
				if tc.email != "" {
					git(t, r.Dir(), "config", "user.email", tc.email)
				}
				got, ok := r.GitIdentity(context.Background())
				if ok != tc.ok || got != tc.want {
					t.Fatalf("GitIdentity = %+v, %v; want %+v, %v", got, ok, tc.want, tc.ok)
				}
			})
		}
	}
}
