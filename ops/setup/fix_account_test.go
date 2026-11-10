package setup

// fix_account_test.go — gh calls for a setup go as its own gh login
// (realmac item 9): GHAccount picks it, and setup.fix hands it to the
// FixRunner.

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

func TestGHAccount(t *testing.T) {
	for _, c := range []struct {
		name string
		gh   onboard.SetupGitHub
		want string
	}{
		{"recorded gh_account wins", onboard.SetupGitHub{User: "mira", GHAccount: "mira-work"}, "mira-work"},
		{"else the GitHub user", onboard.SetupGitHub{User: "mira"}, "mira"},
		{"blank gh_account falls back", onboard.SetupGitHub{User: "mira", GHAccount: "  "}, "mira"},
		{"space trimmed", onboard.SetupGitHub{User: " mira "}, "mira"},
		{"neither: gh's active login", onboard.SetupGitHub{HostAlias: "github-acme"}, ""},
		{"zero entry", onboard.SetupGitHub{}, ""},
	} {
		if got := GHAccount(onboard.SetupEntry{Name: "s", GitHub: c.gh}); got != c.want {
			t.Errorf("%s: GHAccount = %q, want %q", c.name, got, c.want)
		}
	}
	if got := FixGHAccountOf(context.Background()); got != "" {
		t.Errorf("untagged ctx: %q", got)
	}
	if got := FixGHAccountOf(WithFixGHAccount(context.Background(), " mira ")); got != "mira" {
		t.Errorf("tagged ctx: %q, want mira", got)
	}
}

// setup.fix runs a fix (a gh one: gh repo create) as the setup's
// GitHub user, whatever gh has active.
func TestFixRunsAsSetupGHAccount(t *testing.T) {
	for _, c := range []struct {
		gh   onboard.SetupGitHub
		want string
	}{
		{onboard.SetupGitHub{User: "mira"}, "mira"},
		{onboard.SetupGitHub{User: "mira", GHAccount: "mira-work"}, "mira-work"},
	} {
		env := newFakeEnv(onboard.SetupEntry{Name: "acme-hub", Kind: onboard.KindSolo, GitHub: c.gh})
		env.prober.results[onboard.CheckToolsPointerFile] = ProbeResult{Status: onboard.StatusFail}
		if _, err := newTestEngine(env).Fix(yesCtx, FixIn{Setup: "acme-hub", Check: string(onboard.CheckToolsPointerFile)}); err != nil {
			t.Fatal(err)
		}
		if len(env.runner.accounts) != 1 || env.runner.accounts[0] != c.want {
			t.Errorf("%+v: fix ran as %q, want %q", c.gh, env.runner.accounts, c.want)
		}
	}
}
