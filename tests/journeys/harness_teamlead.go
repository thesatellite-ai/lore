//go:build journeys && !windows

package journeys

// harness_teamlead.go — what the team lead's journey needs on top of the
// shared harness: a fake gh and ssh per machine (each person has their own
// GitHub login; the real gh and ssh in /usr/bin must never run, they would
// reach the network), and the setup steps a person saves on the setup page
// to start a team or join one from an invite.
//
//	tlGitHub(m, login)        fake gh/ssh first on PATH, gh's hosts.yml, dead GitHub API
//	tlStartTeam(m, dir, …)    every step of an admin setup (the page's Save, step by step)
//	tlJoin(m, dir, …)         every step of a member setup from an invite line
//
// Placeholders only: logins, emails and urls are made up.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// tlDeadAPI is a GitHub API address nothing listens on: a lookup fails at
// once instead of reaching the network.
const tlDeadAPI = "http://127.0.0.1:9"

// tlGHScript answers the gh calls the setup page makes, for one login.
const tlGHScript = `#!/bin/sh
L="$TL_GH_LOGIN"
case "$1 $2" in
"auth status")
  echo "github.com" >&2
  printf '  ✓ Logged in to github.com account %s (keyring)\n  - Active account: true\n  - Git operations protocol: ssh\n  - Token: placeholder\n' "$L" >&2
  exit 0;;
"auth token") echo "fake-token-$L"; exit 0;;
"api user") printf '{"login":"%s","name":null,"email":null}\n' "$L"; exit 0;;
"api user/emails") echo '[]'; exit 0;;
"api repos/"*) echo true; echo true; exit 0;;
"--version ") echo "gh version 2.60.0 (fake)"; exit 0;;
esac
echo "fake gh: unsupported: $*" >&2
exit 1
`

// tlSSHScript answers ssh -T git@github.com like GitHub, for one login.
const tlSSHScript = `#!/bin/sh
host=""
while [ $# -gt 0 ]; do
  case "$1" in
  -V) echo "OpenSSH_9.6p1 (fake)" >&2; exit 0;;
  -i|-o|-p|-l|-F|-E) shift 2;;
  -*) shift;;
  *) host="$1"; break;;
  esac
done
case "$host" in
*github.com) echo "Hi $TL_GH_LOGIN! You've successfully authenticated, but GitHub does not provide shell access." >&2; exit 1;;
esac
echo "ssh: Could not resolve hostname $host: Name or service not known" >&2
exit 255
`

// tlGitHub gives m a GitHub login: fake gh and ssh first on its PATH, gh's
// hosts.yml naming the login, and a dead GitHub API.
func tlGitHub(t *testing.T, w *World, m *Machine, login string) {
	t.Helper()
	bin := filepath.Join(w.Root, "tlbin", m.Name)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"gh": tlGHScript, "ssh": tlSSHScript} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m.SetEnv("PATH", bin+":"+filepath.Join(w.Root, "shim")+":/usr/bin:/bin")
	m.SetEnv("TL_GH_LOGIN", login)
	m.SetEnv("LORE_GITHUB_API", tlDeadAPI)
	hosts := "github.com:\n    git_protocol: ssh\n    users:\n        " + login + ": {}\n    user: " + login + "\n"
	p := filepath.Join(m.Home, ".config", "gh", "hosts.yml")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(hosts), 0o600); err != nil {
		t.Fatal(err)
	}
}

// tlApply saves one setup step the way the page does; a refusal fails the
// test with what `setup check` says about it.
func tlApply(t testing.TB, m *Machine, dir, setup, step string, answers any) teamharness.Result {
	t.Helper()
	b, err := json.Marshal(answers)
	if err != nil {
		t.Fatal(err)
	}
	r := m.Lore(dir, "setup", "apply", setup, "--step", step, "--answers", string(b), "--json")
	if r.Code != 0 {
		c := m.Lore(dir, "setup", "check", setup, "--step", step, "--answers", string(b), "--json")
		t.Fatalf("setup apply %s --step %s: %s\ncheck says: %s", setup, step, r, c.Stdout)
	}
	return r
}

// tlPerson is what the identity and github steps answer for one person.
type tlPerson struct {
	Name, Email, Login string
}

// tlFirstSteps saves kind, identity, github and personal store.
func tlFirstSteps(t testing.TB, m *Machine, dir, setup, kind string, p tlPerson) {
	t.Helper()
	tlApply(t, m, dir, setup, "kind", map[string]any{"kind": kind, "name": setup})
	tlApply(t, m, dir, setup, "identity", map[string]any{"name": p.Name, "email": p.Email})
	tlApply(t, m, dir, setup, "github", map[string]any{"github_account": p.Login, "host_alias": "github.com"})
	tlApply(t, m, dir, setup, "personal_store", map[string]any{"personal_store": "local"})
}

// tlLastSteps saves tools, secrets, folders, this folder and the summary
// (which ends the new-setup flow).
func tlLastSteps(t testing.TB, m *Machine, dir, setup string, folders []string) {
	t.Helper()
	tlApply(t, m, dir, setup, "tools", map[string]any{"tools": []string{"claude_code"}})
	tlApply(t, m, dir, setup, "secrets", map[string]any{"backend": "none"})
	tlApply(t, m, dir, setup, "folders", map[string]any{"folders": folders, "default": false})
	tlApply(t, m, dir, setup, "this_folder", map[string]any{})
	tlApply(t, m, dir, setup, "summary", map[string]any{})
	// doctor's fix for the tool picked: what the person runs next.
	m.Lore(dir, "skill", "install", "--tool", "claude_code").OK(t)
}

// tlStartTeam saves every step of an admin setup that starts org on storeURL.
func tlStartTeam(t testing.TB, m *Machine, dir, setup, org, storeURL string, p tlPerson, folders []string) {
	t.Helper()
	tlFirstSteps(t, m, dir, setup, "admin", p)
	tlApply(t, m, dir, setup, "team", map[string]any{"org": org, "store_url": storeURL})
	tlLastSteps(t, m, dir, setup, folders)
}

// tlJoin saves every step of a member setup from an invite line.
func tlJoin(t testing.TB, m *Machine, dir, setup, invite string, p tlPerson, folders []string) {
	t.Helper()
	tlFirstSteps(t, m, dir, setup, "member", p)
	tlApply(t, m, dir, setup, "join", map[string]any{"invite": invite})
	tlLastSteps(t, m, dir, setup, folders)
}

// tlInvite runs `team invite` and returns the invite line it prints.
func tlInvite(t testing.TB, m *Machine, dir, email string, extra ...string) (id, line string) {
	t.Helper()
	r := m.Lore(dir, append([]string{"team", "invite", email, "--yes", "--json"}, extra...)...).OK(t)
	var o struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &o); err != nil || !strings.HasPrefix(o.Text, "lore-invite:") {
		t.Fatalf("team invite printed no invite line: %s", r)
	}
	return o.ID, o.Text
}
