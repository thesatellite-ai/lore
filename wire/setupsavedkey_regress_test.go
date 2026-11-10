package wire_test

// setupsavedkey_regress_test.go — a saved setup whose recorded key (or
// alias) logs in as another GitHub account is flagged by
// github.key_matches, with a runnable fix that makes it log in as the
// setup's own account.

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// Finding d01a1fab→891b57d9 (realmac NEW-2): a saved setup for one account
// recorded a key that logs in as another; the GitHub step's route line
// still showed the route green, and the fix that existed pinned yet another
// key. github.key_matches must fail and name both accounts (the route line
// reads it), and its Run it must leave a setup that logs in as its own
// account: with the setup's alias already right, by dropping the recorded
// key (what github.ssh_user's text promises).
func TestRegress_SavedKeyFromOtherAccountFlagged(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	cases := []struct {
		name, github string
		noPub        bool
		wantArg      string // an argument the runnable fix must carry
	}{
		{"recorded key, .pub beside it", "{user: octo-personal, route: ssh, host_alias: github.com, ssh_key: ~/.ssh/work-key}", false, "--no-ssh-key"},
		{"recorded key, no .pub", "{user: octo-personal, route: ssh, host_alias: github.com, ssh_key: ~/.ssh/work-key}", true, "--no-ssh-key"},
		{"alias of the other account", "{user: octo-personal, route: ssh, host_alias: work-gh}", false, "github.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newRegressWorld(t, rwShape{GHAccounts: 2, SSH: "several", NoPub: c.noPub})
			w.WriteConfig("setups:\n  - name: personal\n    kind: solo\n    default: true\n" +
				"    identity: {key: \"human:person@example.com\", name: Pat Example}\n    github: " + c.github + "\n")
			km := rwKeyMatches(t, w)
			if km.Status != "fail" {
				t.Fatalf("github.key_matches = %s (%s); the saved route logs in as octo-work", km.Status, km.Detail)
			}
			if !strings.Contains(km.Detail, "octo-work") || !strings.Contains(km.Detail, "octo-personal") {
				t.Errorf("key_matches detail does not name both accounts: %s", km.Detail)
			}
			if !km.Fix.Runnable || !slices.Contains(km.Fix.Command, c.wantArg) {
				t.Fatalf("key_matches fix %q %q: want a runnable fix with %s", km.Fix.Text, km.Fix.Command, c.wantArg)
			}
			// Run it: the fix is a `lore setup edit` line; the test runs the
			// same op in process (a child lore would wait on this App's lock).
			rwRunEditFix(t, w, km.Fix.Command)
			w.Reopen()
			if strings.Contains(w.Config(), "work-key") || strings.Contains(w.Config(), "work-gh") {
				t.Errorf("after Run it the setup still routes through the other account's key or alias:\n%s", w.Config())
			}
			if again := rwKeyMatches(t, w); again.Status != "ok" {
				t.Errorf("after Run it github.key_matches = %s: %s", again.Status, again.Detail)
			}
		})
	}
}

// rwKeyMatches checks the saved setup's GitHub step (the plan's values,
// as the page sends them) and returns github.key_matches.
func rwKeyMatches(t *testing.T, w *rwWorld) rwCheck {
	t.Helper()
	st, ok := w.Plan("personal").Step("github")
	if !ok {
		t.Fatal("no github step")
	}
	for _, c := range w.Check("personal", "github", st.Prefilled()).Checks {
		if c.Check == "github.key_matches" {
			return c
		}
	}
	t.Fatal("no github.key_matches row")
	return rwCheck{}
}

// rwRunEditFix runs a `lore setup edit <name> --flag value …` fix command
// as the setup.edit op.
func rwRunEditFix(t *testing.T, w *rwWorld, argv []string) {
	t.Helper()
	if len(argv) < 4 || argv[0] != "lore" || argv[1] != "setup" || argv[2] != "edit" {
		t.Fatalf("fix %q is not a lore setup edit line", argv)
	}
	type editIn struct {
		Name      string `json:"name"`
		HostAlias string `json:"host_alias,omitempty"`
		SSHKey    string `json:"ssh_key,omitempty"`
		Route     string `json:"route,omitempty"`
		NoSSHKey  bool   `json:"no_ssh_key,omitempty"`
	}
	in := editIn{Name: argv[3]}
	for i := 4; i < len(argv); i++ {
		switch argv[i] {
		case "--no-ssh-key":
			in.NoSSHKey = true
		case "--host-alias", "--ssh-key", "--route":
			if i+1 >= len(argv) {
				t.Fatalf("fix %q: %s has no value", argv, argv[i])
			}
			v := argv[i+1]
			switch argv[i] {
			case "--host-alias":
				in.HostAlias = v
			case "--ssh-key":
				in.SSHKey = v
			default:
				in.Route = v
			}
			i++
		default:
			t.Fatalf("fix %q: flag %s not handled by the test", argv, argv[i])
		}
	}
	var out rwNone
	rwMust(w, "setup.edit", in, &out)
}
