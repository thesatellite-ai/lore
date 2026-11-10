//go:build journeys && !windows

package journeys

// land7_c_test.go — the solo dev's config.yaml from an older lore
// (decided 2026-11-10, land7 C):
//
//	day 1    Sam's laptop carries a config.yaml an older lore wrote: the
//	         setups on one line, and a GitHub block with no route and no
//	         key fingerprint. Doctor warns config_style; setup.list (Home on
//	         the setup page) names the keys with the fix lore config fmt.
//	         Sam presses Run it: the page calls config.fmt, the file is in
//	         block style, the setup is kept and the warning is gone.
//	week 1   Sam saves his GitHub settings unchanged (setup edit with the
//	         same account and alias): the first such save records route ssh
//	         and the key's fingerprint, so doctor's key check has something
//	         to compare; the second writes nothing. He swaps the key behind
//	         github.com; another unchanged save keeps the recorded
//	         fingerprint, so doctor still sees the swap.
//
// Named TestJourneySolo… so the solo persona's gate step runs it.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Placeholders of this journey.
const (
	l7cName  = "Sam Solo"
	l7cEmail = "sam@solo.example"
	l7cLogin = "sam-dev"
	l7cSetup = "personal"
)

// l7cOldConfig is config.yaml as an older lore left it: setups on one flow
// line, a github block with the account, the alias and a .pub key path
// (as saved before 2026-11-07), no route and no key fingerprint.
const l7cOldConfig = "# Sam's laptop, written by an older lore\n" +
	"setups: [{name: " + l7cSetup + ", kind: solo, default: true, folders: [~/code/**], " +
	"identity: {key: \"human:" + l7cEmail + "\", name: " + l7cName + ", emails: [" + l7cEmail + "]}, " +
	"github: {user: " + l7cLogin + ", host_alias: github.com, ssh_key: ~/.ssh/id_ed25519.pub}, personal_store: none}]\n"

// l7cList is setup.list's JSON as the page reads it.
type l7cList struct {
	Setups      []struct{ Name string } `json:"setups"`
	ConfigStyle struct {
		Keys    []string `json:"keys"`
		Command []string `json:"command"`
	} `json:"config_style"`
}

func l7cReadList(t testing.TB, m *Machine) l7cList {
	t.Helper()
	var l l7cList
	r := m.Lore("", "setup", "list", "--json").OK(t)
	if err := json.Unmarshal([]byte(r.Stdout), &l); err != nil {
		t.Fatalf("setup list --json: %v\n%s", err, r.Stdout)
	}
	return l
}

// l7cGitHub is the saved github block of Sam's setup (setup show).
func l7cGitHub(t testing.TB, m *Machine) (route, fingerprint string) {
	t.Helper()
	r := m.Lore("", "setup", "show", l7cSetup, "--json").OK(t)
	var s struct {
		Setup struct {
			GitHub struct {
				Route          string `json:"route"`
				KeyFingerprint string `json:"key_fingerprint"`
			} `json:"github"`
		} `json:"setup"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &s); err != nil {
		t.Fatalf("setup show --json: %v\n%s", err, r.Stdout)
	}
	return s.Setup.GitHub.Route, s.Setup.GitHub.KeyFingerprint
}

// l7cEdit runs setup edit on Sam's setup and reports whether it wrote.
func l7cEdit(t testing.TB, m *Machine, flags ...string) bool {
	t.Helper()
	r := m.Lore("", append(append([]string{"setup", "edit", l7cSetup}, flags...), "--json")...).OK(t)
	var out struct {
		Changed bool `json:"changed"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("setup edit --json: %v\n%s", err, r.Stdout)
	}
	return out.Changed
}

func TestJourneySoloLand7C(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(l7cName, l7cEmail)
	lap := sam.Laptop()
	cfgPath := filepath.Join(lap.Home, ".lore", "config.yaml")
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// ── day 1 ────────────────────────────────────────────────────────────
	w.Step("day1/old-config-on-one-line", func() {
		lap.GitHubAccounts(t, GitHubAccount{Login: l7cLogin, Key: "id_ed25519"})
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfgPath, []byte(l7cOldConfig), 0o600); err != nil {
			t.Fatal(err)
		}
		lap.Lore("", "doctor").Says(t, "config_style", "lore config fmt")
		l := l7cReadList(t, lap)
		if !slices.Contains(l.ConfigStyle.Keys, "setups") || strings.Join(l.ConfigStyle.Command, " ") != "lore config fmt" {
			t.Errorf("Home (setup.list) shows no config style box for setups on one line: %+v", l.ConfigStyle)
		}
	})

	w.Step("day1/run-it-config-fmt", func() {
		// The page's Run it calls config.fmt, the op behind lore config fmt.
		lap.Lore("", "config", "fmt").OK(t).Says(t, "block style")
		after := read()
		if strings.Contains(after, "[{") || !strings.Contains(after, "# Sam's laptop") {
			t.Errorf("config.yaml after Run it (one line left, or the comment lost):\n%s", after)
		}
		l := l7cReadList(t, lap)
		if len(l.ConfigStyle.Keys) != 0 || len(l.Setups) != 1 || l.Setups[0].Name != l7cSetup {
			t.Errorf("after Run it: config style %+v, setups %+v", l.ConfigStyle, l.Setups)
		}
		lap.Lore("", "doctor").Never(t, "config_style")
	})

	// ── week 1 ───────────────────────────────────────────────────────────
	w.AdvanceDays(6)
	var recorded string
	w.Step("week1/unchanged-github-save-records-route-and-key", func() {
		if route, fp := l7cGitHub(t, lap); route != "" || fp != "" {
			t.Fatalf("the old block already has route %q fingerprint %q", route, fp)
		}
		if !l7cEdit(t, lap, "--github-user", l7cLogin, "--host-alias", "github.com", "--ssh-key", "~/.ssh/id_ed25519.pub") {
			t.Error("the first unchanged GitHub save wrote nothing")
		}
		route, fp := l7cGitHub(t, lap)
		if route != "ssh" || !strings.HasPrefix(fp, "SHA256:") {
			t.Fatalf("the first unchanged GitHub save recorded route %q fingerprint %q, want ssh and the key's", route, fp)
		}
		recorded = fp
		before := read()
		if l7cEdit(t, lap, "--github-user", l7cLogin, "--host-alias", "github.com", "--ssh-key", "~/.ssh/id_ed25519.pub") || read() != before {
			t.Error("the second unchanged GitHub save rewrote config.yaml")
		}
	})

	w.Step("week1/key-swapped-then-unchanged-save", func() {
		key := filepath.Join(lap.Home, ".ssh", "id_ed25519")
		for _, f := range []string{key, key + ".pub"} {
			if err := os.Remove(f); err != nil {
				t.Fatal(err)
			}
		}
		if out, err := exec.Command("/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "swapped@placeholder", "-f", key).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v %s", err, out)
		}
		if l7cEdit(t, lap, "--github-user", l7cLogin, "--ssh-key", "~/.ssh/id_ed25519.pub") {
			t.Error("an unchanged save after the key swap wrote config.yaml")
		}
		if _, fp := l7cGitHub(t, lap); fp != recorded {
			t.Errorf("an unchanged save replaced the recorded fingerprint %q with %q: the swapped key would go unseen", recorded, fp)
		}
	})
}
