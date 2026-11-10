package wire

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// storeRouteConfig: a work setup owning the acme org's store and its own
// personal store through the github-work alias, a personal setup owning
// alice's notes, a disabled setup, and two setups sharing one store with
// different aliases (the identity cases of storeRoute).
const storeRouteConfig = `personal:
  store: git@github.com:alice/lore-me.git
orgs:
  - name: acme
    stores:
      - url: git@github.com:acme/hub.git
setups:
  - name: work
    kind: member
    org: acme
    personal_store: git@github-work:alice-work/me.git
    github: {user: alice-work, host_alias: github-work}
  - name: personal
    kind: solo
    personal_store: https://github.com/alice/notes.git
    github: {user: alice, host_alias: github-alice}
  - name: off
    kind: solo
    disabled: true
    personal_store: git@github.com:alice/old.git
    github: {user: alice, host_alias: github-old}
  - name: a
    kind: solo
    personal_store: git@github.com:shared/team.git
    github: {user: one, host_alias: github-one}
  - name: b
    kind: solo
    personal_store: git@github.com:shared/team.git
    github: {user: two, host_alias: github-two}
  - name: home
    kind: solo
    github: {user: alice, host_alias: github-home}
`

func TestStoreRoute(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(storeRouteConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	insteadOf := func(alias string) []string {
		return []string{"-c", "url.git@" + alias + ":.insteadOf=git@github.com:", "-c", "url.ssh://git@" + alias + "/.insteadOf=ssh://git@github.com/"}
	}
	cases := []struct {
		name, url string
		want      []string
		ok        bool
	}{
		{"org store of a member setup", "git@github.com:acme/hub.git", insteadOf("github-work"), true},
		{"org store, ssh:// spelling and case", "ssh://git@github.com/Acme/Hub", insteadOf("github-work"), true},
		{"org store, spelled with the setup's own alias", "git@github-work:acme/hub.git", insteadOf("github-work"), true},
		{"personal store spelled with the alias, asked as github.com", "git@github.com:alice-work/me.git", insteadOf("github-work"), true},
		{"https personal store of the solo setup", "git@github.com:alice/notes.git", insteadOf("github-alice"), true},
		{"same path on another host is another store", "git@gitlab.com:acme/hub.git", nil, false},
		{"same path behind another setup's alias is not this setup's", "git@github-alice:acme/hub.git", nil, false},
		{"personal.store is owned by the setup that inherits it (unset = inherit)", "git@github.com:alice/lore-me.git", insteadOf("github-home"), true},
		{"personal.store behind the inheriting setup's alias", "git@github-home:alice/lore-me.git", insteadOf("github-home"), true},
		{"a store no setup owns", "git@github.com:acme/site.git", nil, false},
		{"a disabled setup owns nothing", "git@github.com:alice/old.git", nil, false},
		{"two owners disagree and none is active", "git@github.com:shared/team.git", nil, false},
		{"a local store", "/srv/stores/hub.git", nil, false},
		{"empty", "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args, env, ok := storeRoute(context.Background(), home, t.TempDir(), c.url)
			if ok != c.ok || !slices.Equal(args, c.want) || len(env) != 0 {
				t.Errorf("storeRoute(%q) = %q %q %v, want %q %v", c.url, args, env, ok, c.want, c.ok)
			}
		})
	}
	t.Run("the active setup breaks a tie", func(t *testing.T) {
		t.Setenv("LORE_SETUP", "b")
		args, _, ok := storeRoute(context.Background(), home, t.TempDir(), "git@github.com:shared/team.git")
		if !ok || !slices.Equal(args, insteadOf("github-two")) {
			t.Errorf("tie with b active = %q %v", args, ok)
		}
	})
	t.Run("the clone path gives a store no setup owns no alias, even with a setup active", func(t *testing.T) {
		t.Setenv("LORE_SETUP", "personal")
		if args, env := cloneRoute(context.Background(), home, t.TempDir(), "git@github.com:acme/site.git"); len(args)+len(env) != 0 {
			t.Errorf("clone route of an unowned store = %q %q, want none", args, env)
		}
		if args, _ := cloneRoute(context.Background(), home, t.TempDir(), "git@github.com:acme/hub.git"); !slices.Equal(args, insteadOf("github-work")) {
			t.Errorf("clone route of acme's store under the personal setup = %q, want the owner's", args)
		}
	})
}
