package team

import (
	"context"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/setup"
)

// aliasSSHConfig answers like a ~/.ssh/config with `Host work-gh` /
// `HostName github.com`, for the test only.
func aliasSSHConfig(t *testing.T) {
	t.Helper()
	gitbackend.SetAliasHosts(func(host string) (string, bool) {
		if host == "work-gh" {
			return "github.com", true
		}
		return "", false
	})
	t.Cleanup(func() { gitbackend.SetAliasHosts(nil) })
}

// TestJoinAliasSpellingIsTheSameStore (devco D-626): an invite whose store
// is spelled through an ssh host alias joins the org entry made for the
// github.com spelling of the same store; it was E_SETUP_ORG_MISMATCH.
func TestJoinAliasSpellingIsTheSameStore(t *testing.T) {
	aliasSSHConfig(t)
	x := newFixture(t)
	out := x.invite(setup.TeamInviteIn{Email: "tomas@agency.example"})
	inv, err := onboard.DecodeInvite(out.Line)
	if err != nil {
		t.Fatal(err)
	}
	inv.Store = "git@work-gh:agency/lore-team.git"
	line, err := onboard.EncodeInvite(inv)
	if err != nil {
		t.Fatal(err)
	}
	x.joiner()
	if _, err = x.join(line, "tomas@agency.example"); err != nil {
		t.Fatalf("join through the alias: %v", err)
	}
	if len(x.orgs.imports) != 0 {
		t.Error("imported again over the entry of the same store")
	}
	if got := x.e.origins["agency"].Store; got != storeURL {
		t.Errorf("the entry's url = %q, want it kept as %q", got, storeURL)
	}
}

// TestCheckJoinEntry: the read-only org-entry check a setup apply runs
// before its first write (devco D-626).
func TestCheckJoinEntry(t *testing.T) {
	aliasSSHConfig(t)
	ctx := context.Background()
	x := newFixture(t)
	for _, c := range []struct {
		org, store string
		have       bool
		code       errs.Code
	}{
		{"agency", storeURL, true, ""},
		{"agency", "git@work-gh:agency/lore-team.git", true, ""},
		{"agency", "https://github.com/agency/lore-team", true, ""},
		{"agency", "git@github.com:other/lore-team.git", false, errs.SetupOrgMismatch},
		{"agency", "git@work-gh:other/lore-team.git", false, errs.SetupOrgMismatch},
		{"nobody", "git@github.com:other/lore-team.git", false, ""},
	} {
		have, err := CheckJoinEntry(ctx, x.e, c.org, c.store)
		if c.code != "" {
			wantCode(t, c.store, err, c.code)
			continue
		}
		if err != nil || have != c.have {
			t.Errorf("%s %s: have=%v err=%v, want have=%v", c.org, c.store, have, err, c.have)
		}
	}
}

// TestCheckJoinEntryAliasNoSSHConfig (devco D-680): on a machine whose ssh
// config lore reads does not name the alias (git reaches the store by
// url.<base>.insteadOf), the alias spelling of the entry's store is still
// that store: the join refused it E_SETUP_ORG_MISMATCH against the entry
// its own import had just made. A dotted host the ssh config does not
// name stays another store.
func TestCheckJoinEntryAliasNoSSHConfig(t *testing.T) {
	gitbackend.SetAliasHosts(nil)
	ctx := context.Background()
	x := newFixture(t)
	for _, c := range []struct {
		store string
		code  errs.Code
	}{
		{"git@work-gh:agency/lore-team.git", ""},
		{"ssh://git@work-gh/agency/lore-team.git", ""},
		{"git@git.other.example:agency/lore-team.git", errs.SetupOrgMismatch},
		{"git@work-gh:other/lore-team.git", errs.SetupOrgMismatch},
	} {
		have, err := CheckJoinEntry(ctx, x.e, "agency", c.store)
		if c.code != "" {
			wantCode(t, c.store, err, c.code)
			continue
		}
		if err != nil || !have {
			t.Errorf("%s: have=%v err=%v, want the entry's own store", c.store, have, err)
		}
	}
}
