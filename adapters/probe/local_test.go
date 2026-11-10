package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/secretmgr"
)

// GHLogins reads gh's logins from hosts.yml (multi-login and older
// one-login forms) without running gh; a missing file or host is
// not_logged_in, a broken file is failed.
func TestGHLogins(t *testing.T) {
	cases := []struct {
		name, yml string
		class     Class
		users     []string
		active    string
	}{
		{"multi", "github.com:\n  users:\n    mira:\n      oauth_token: x\n    acme-bot:\n  user: mira\n", ClassOK, []string{"acme-bot", "mira"}, "mira"},
		{"single", "github.com:\n  user: mira\n  oauth_token: x\n", ClassOK, []string{"mira"}, "mira"},
		{"other host", "gitlab.com:\n  user: mira\n", ClassNotLoggedIn, nil, ""},
		{"broken", "github.com: [\n", ClassFailed, nil, ""},
		{"no file", "", ClassNotLoggedIn, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSandbox(t)
			if c.yml != "" {
				dir := filepath.Join(s.home, ".config", "gh")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ghHostsFile), []byte(c.yml), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := s.prober(Options{}).GHLogins()
			if got.Class != c.class || got.Active != c.active || len(got.Accounts) != len(c.users) {
				t.Fatalf("got %+v", got)
			}
			for i, u := range c.users {
				if got.Accounts[i].User != u || got.Accounts[i].Active != (u == c.active) {
					t.Errorf("account %d: %+v", i, got.Accounts[i])
				}
			}
		})
	}
}

// FindSecrets looks programs up on PATH and runs none of them.
func TestFindSecretsRunsNothing(t *testing.T) {
	s := newSandbox(t)
	log := filepath.Join(s.home, "calls")
	fakeBin(t, s.bin, "gopass", `echo ran >> `+log)
	got := map[string]SecretsDetection{}
	for _, d := range s.prober(Options{}).FindSecrets() {
		got[d.Manager] = d
	}
	if d := got[secretmgr.Gopass]; d.Class != ClassOK || d.Path == "" || d.Answers {
		t.Errorf("gopass: %+v", d)
	}
	if d := got[secretmgr.Vault]; d.Class != ClassToolMissing {
		t.Errorf("vault: %+v", d)
	}
	if _, err := os.Stat(log); err == nil {
		t.Errorf("FindSecrets ran a program")
	}
}
