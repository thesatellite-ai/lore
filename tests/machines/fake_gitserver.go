//go:build machines && !windows

package machines

// fake_gitserver.go — what a team setup needs from GitHub on a machine
// with no network: the team store's GitHub url, which git rewrites
// (url.insteadOf in the machine's ~/.gitconfig) to a bare repo on this
// disk, so lore sees the url a real team has and two machines (admin and
// member) share one repo; and gh's hosts.yml for the fake gh's logins,
// which the https route reads. Any test (CLI or REST) can use it:
//
//	bare := <git init --bare>; tm := lsTeam{storeURL: lsTeamURL, bare: bare}
//	tm.rewrite(t, m); lsGHHosts(t, m)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lsTeam is the org store and invite a member or admin setup uses.
type lsTeam struct {
	storeURL string
	invite   string
	bare     string
}

// lsTeamURL is the team store's url as the team knows it.
const lsTeamURL = "git@github.com:lifeorg/lore-team.git"

// rewrite points m's git at the bare repo for lsTeamURL.
func (tm lsTeam) rewrite(t *testing.T, m *Machine) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(m.Home, ".gitconfig"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("[url \"file://" + tm.bare + "\"]\n\tinsteadOf = " + lsTeamURL + "\n"); err != nil {
		t.Fatal(err)
	}
}

// lsGHHosts writes gh's hosts.yml for the fake gh's logins, as a real gh
// login leaves it (the https route reads it; the fixture's fake gh has
// only its own data file).
func lsGHHosts(t *testing.T, m *Machine) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(m.Dir, "gh-accounts"))
	if err != nil {
		t.Fatal(err)
	}
	var users, active strings.Builder
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(l)
		if len(f) != 2 {
			continue
		}
		users.WriteString("            " + f[0] + ": {}\n")
		if f[1] == "active" {
			active.WriteString(f[0])
		}
	}
	body := "github.com:\n    git_protocol: ssh\n    users:\n" + users.String() + "    user: " + active.String() + "\n"
	// gh's own lookup: XDG_CONFIG_HOME when the shape sets one.
	cfg := filepath.Join(m.Home, ".config")
	for _, kv := range m.ExtraEnv {
		if v, ok := strings.CutPrefix(kv, "XDG_CONFIG_HOME="); ok && v != "" {
			cfg = v
		}
	}
	p := filepath.Join(cfg, "gh", "hosts.yml")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
