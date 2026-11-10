package onboard

import (
	"strings"
	"testing"
)

// KeyMatch: the owner's real mix-up (a key of octo-work recorded with
// the github.com alias of octo-personal) fails and names both accounts; the
// matching pair passes; a missing key file fails; no answer is a warning.
func TestKeyMatch(t *testing.T) {
	const home = "/h"
	id_personal := KeyMatchIn{User: "octo-personal", Alias: "github.com", AliasKeys: []string{home + "/.ssh/id_personal"}, AliasKeyShown: "~/.ssh/id_personal",
		AliasLogin: "octo-personal", AliasAnswered: true}
	with := func(base KeyMatchIn, f func(*KeyMatchIn)) KeyMatchIn { f(&base); return base }
	cases := []struct {
		name   string
		in     KeyMatchIn
		status Status
		want   []string
	}{
		{"no key recorded: the alias decides", id_personal, StatusOK, []string{"github.com logs in as octo-personal"}},
		{"no key recorded, alias logs in as another", with(id_personal, func(i *KeyMatchIn) { i.User = "octo-work" }), StatusFail,
			[]string{"github.com logs in as octo-personal, but this setup's account is octo-work"}},
		{"no key recorded, alias refused", with(id_personal, func(i *KeyMatchIn) { i.AliasLogin = "" }), StatusFail, []string{"GitHub refuses"}},
		{"no key recorded, alias silent", with(id_personal, func(i *KeyMatchIn) { i.AliasLogin, i.AliasAnswered = "", false }), StatusWarn, []string{"gave no answer"}},
		{"private key path recorded (no .pub)", with(id_personal, func(i *KeyMatchIn) {
			i.Key, i.KeyFile, i.KeyExists = "~/.ssh/id_personal", home+"/.ssh/id_personal", true
			i.KeyLogin, i.KeyAnswered = "octo-personal", true
		}), StatusOK, []string{"id_personal logs in as octo-personal"}},
		{"fingerprint swapped since save", with(id_personal, func(i *KeyMatchIn) {
			i.Fingerprint, i.FingerprintNow = "SHA256:old", "SHA256:new"
		}), StatusFail, []string{"SHA256:new, not SHA256:old"}},
		{"fingerprint same", with(id_personal, func(i *KeyMatchIn) {
			i.Fingerprint, i.FingerprintNow = "SHA256:same", "SHA256:same"
		}), StatusOK, nil},
		{"https with gh login", KeyMatchIn{User: "octo-user", Route: RouteHTTPS, GHHasLogin: true}, StatusOK, []string{"over https"}},
		{"https without gh login", KeyMatchIn{User: "octo-user", Route: RouteHTTPS}, StatusFail, []string{"gh auth login"}},
		{"owner's mix-up: other account's key, other alias", with(id_personal, func(i *KeyMatchIn) {
			i.Key, i.KeyFile, i.KeyExists = "~/.ssh/id_work.pub", home+"/.ssh/id_work", true
			i.KeyLogin, i.KeyAnswered = "octo-work", true
		}), StatusFail, []string{"id_work logs in as octo-work", "this setup's account is octo-personal", "github.com uses ~/.ssh/id_personal, which logs in as octo-personal"}},
		{"right account's key but not the alias's", with(id_personal, func(i *KeyMatchIn) {
			i.Key, i.KeyFile, i.KeyExists = "~/.ssh/id_ed25519.pub", home+"/.ssh/id_ed25519", true
			i.KeyLogin, i.KeyAnswered = "octo-personal", true
		}), StatusFail, []string{"never uses the recorded key"}},
		{"matching pair", with(id_personal, func(i *KeyMatchIn) {
			i.Key, i.KeyFile, i.KeyExists = "~/.ssh/id_personal.pub", home+"/.ssh/id_personal", true
			i.KeyLogin, i.KeyAnswered = "octo-personal", true
		}), StatusOK, []string{"logs in as octo-personal"}},
		{"alias's key, wrong account", with(id_personal, func(i *KeyMatchIn) {
			i.User = "octo-work"
			i.Key, i.KeyFile, i.KeyExists = "~/.ssh/id_personal.pub", home+"/.ssh/id_personal", true
			i.KeyLogin, i.KeyAnswered = "octo-personal", true
		}), StatusFail, []string{"id_personal logs in as octo-personal, but this setup's account is octo-work"}},
		{"key file missing", with(id_personal, func(i *KeyMatchIn) {
			i.Key, i.KeyFile = "~/.ssh/id_personal.pub", home+"/.ssh/id_personal"
		}), StatusFail, []string{"key file missing"}},
		{"refused", with(id_personal, func(i *KeyMatchIn) {
			i.Key, i.KeyFile, i.KeyExists, i.KeyAnswered = "~/.ssh/id_personal.pub", home+"/.ssh/id_personal", true, true
		}), StatusFail, []string{"GitHub refuses"}},
		{"no answer is a warning", with(id_personal, func(i *KeyMatchIn) {
			i.Key, i.KeyFile, i.KeyExists = "~/.ssh/id_personal.pub", home+"/.ssh/id_personal", true
		}), StatusWarn, []string{"gave no answer"}},
		{"alias with default keys: only the account counts", KeyMatchIn{User: "octo-personal", Alias: "github.com",
			Key: "~/.ssh/x.pub", KeyFile: home + "/.ssh/x", KeyExists: true, KeyLogin: "octo-personal", KeyAnswered: true}, StatusOK, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, msg := KeyMatch(c.in)
			if st != c.status {
				t.Fatalf("status %s, want %s (%s)", st, c.status, msg)
			}
			for _, w := range c.want {
				if !strings.Contains(msg, w) {
					t.Errorf("message %q lacks %q", msg, w)
				}
			}
		})
	}
}

// ChooseRoute: the route order of the owner's 2026-11-07 rule, table-tested
// over the shapes a real ~/.ssh has (the default host with a key that has no
// .pub, a broken alias, two accounts, gh logins, a probe still running).
func TestChooseRoute(t *testing.T) {
	def := RouteAlias{Alias: GitHubHostName, KeyFile: "~/.ssh/main-key", LogsInAs: "octo-user"}
	broken := RouteAlias{Alias: "octo-user-gh", KeyFile: "/Users/elsewhere/.ssh/octo-user-gh", KeyMissing: true}
	work := RouteAlias{Alias: "work-gh", KeyFile: "~/.ssh/work-key", LogsInAs: "work-user"}
	cases := []struct {
		name    string
		user    string
		aliases []RouteAlias
		gh      []string
		route   GitRoute
		alias   string
		line    string
	}{
		{"default host logs in, broken alias ignored", "octo-user", []RouteAlias{broken, def, work}, nil, RouteSSH, GitHubHostName, "via your ssh config (github.com, key main-key)"},
		{"named alias before github.com", "octo-user", []RouteAlias{def, {Alias: "octo-gh", KeyFile: "~/.ssh/k.pub", LogsInAs: "OCTO-USER"}}, nil, RouteSSH, "octo-gh", "(alias octo-gh, key k)"},
		{"other account via its alias", "work-user", []RouteAlias{def, work}, []string{"work-user"}, RouteSSH, "work-gh", "alias work-gh"},
		{"no ssh route: https via gh", "third-user", []RouteAlias{def, work}, []string{"octo-user", "third-user"}, RouteHTTPS, "", "over https"},
		{"neither: fix", "third-user", []RouteAlias{def}, []string{"octo-user"}, "", "", "no way to reach GitHub as third-user"},
		{"pending before https", "third-user", []RouteAlias{def, {Alias: "slow", Pending: true}}, []string{"third-user"}, "", "", "still asking slow"},
		{"no account", "", []RouteAlias{def}, nil, "", "", "pick the GitHub account first"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ChooseRoute(c.user, c.aliases, c.gh)
			if got.Route != c.route || got.Alias != c.alias || !strings.Contains(got.Line, c.line) {
				t.Fatalf("ChooseRoute = %+v, want route %q alias %q line ~%q", got, c.route, c.alias, c.line)
			}
			if !got.Found() && got.Fix == "" {
				t.Errorf("no route and no fix: %+v", got)
			}
		})
	}
}
