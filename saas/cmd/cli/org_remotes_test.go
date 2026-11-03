package main

import (
	"context"
	"os/exec"
	"reflect"
	"sort"
	"testing"

	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/repourl"
)

// Remotes are read as written in git config, never through url.insteadOf
// (F10 edge cases, Q2): a mirror rewrite must not change the org match.
func TestGitRemotesIgnoreInsteadOf(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(cmd.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("remote", "add", "origin", "https://github.com/acme/app")
	git("config", "remote.origin.pushurl", "git@github.com:acme/app-push.git")
	git("remote", "add", "up.stream", "https://gitlab.com/acme/upstream.git")
	// Every hosted URL is rewritten to a local path, as a mirror setup does.
	git("config", "url./mirror/github/.insteadOf", "https://github.com/")
	git("config", "--add", "url./mirror/gh-ssh/.insteadOf", "git@github.com:")
	git("config", "url./mirror/gitlab/.insteadOf", "https://gitlab.com/")

	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	got, notes := gitRemotes(context.Background(), dir)
	var want []string
	for _, u := range []string{"https://github.com/acme/app", "git@github.com:acme/app-push.git", "https://gitlab.com/acme/upstream.git"} {
		n, err := repourl.Normalize(u)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, n)
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) || len(notes) != 0 {
		t.Fatalf("gitRemotes = %v (notes %v), want %v and no notes", got, notes, want)
	}
	if o := readGitOriginURL(dir); o != "https://github.com/acme/app" {
		t.Fatalf("readGitOriginURL = %q, want the raw origin URL", o)
	}
	if rs := rawRemoteURLs(context.Background(), dir, "up.stream"); len(rs) != 1 || rs[0].Name != "up.stream" {
		t.Fatalf("dotted remote name = %+v", rs)
	}
	if rs := rawRemoteURLs(context.Background(), t.TempDir(), ""); rs != nil {
		t.Fatalf("not a repo: %+v, want nil", rs)
	}
}

// whoami's "org none" hint names only entries a user may pick casually:
// never a staging entry (Q-a), the entry itself when there is one.
func TestOrgUseHint(t *testing.T) {
	prod := lorecfg.Org{Name: "northwind", Stores: []lorecfg.Store{{URL: "https://github.com/northwind/lore"}}}
	stag := lorecfg.Org{Name: "northwind-stag", Stores: []lorecfg.Store{{URL: "https://github.com/northwind/lore", Branch: "staging"}}}
	beta := lorecfg.Org{Name: "betalabs", Stores: []lorecfg.Store{{URL: "https://github.com/betalabs/lore"}}}
	cases := []struct {
		name string
		cfg  *lorecfg.Config
		want string
	}{
		{"no config", nil, ""},
		{"no orgs", &lorecfg.Config{}, ""},
		{"only staging", &lorecfg.Config{Orgs: []lorecfg.Org{stag}}, ""},
		{"one plus staging", &lorecfg.Config{Orgs: []lorecfg.Org{prod, stag}}, "lore org use northwind"},
		{"two", &lorecfg.Config{Orgs: []lorecfg.Org{prod, stag, beta}}, "lore org use <entry> (northwind, betalabs)"},
	}
	for _, c := range cases {
		if got := orgUseHint(c.cfg); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
