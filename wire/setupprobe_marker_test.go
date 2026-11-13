package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// store.marker's fix names the checked store, never a fixed "me": a doctor
// target "store agency" (an admin setup's org store) fills agency, the
// personal store step (no store target) fills me (B4-postmerge).
func TestStoreMarkerFixNamesTheStore(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ target, want string }{
		{"", "me"},
		{opssetup.TargetStorePrefix + "agency", "agency"},
		{opssetup.TargetStorePrefix + "me", "me"},
		{"file:///srv/stores/agency", "me"},
	} {
		if got := markerStoreName(opssetup.ProbeRequest{Target: tc.target}); got != tc.want {
			t.Errorf("markerStoreName(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
	for _, name := range []string{"me", "agency"} {
		f, err := onboard.FillFix(onboard.CheckStoreMarker, onboard.FixFor(onboard.CheckStoreMarker, onboard.StepPersonalStore, onboard.FixDefault),
			map[string]string{onboard.VarStoreName: name, onboard.VarURL: "git@github.com:acme/notes.git"}, onboard.Step{})
		if err != nil {
			t.Fatal(err)
		}
		if want := "lore store init --name " + name + " --url git@github.com:acme/notes.git"; !strings.Contains(f.Text, want) {
			t.Errorf("fix for %s = %q, want it to contain %q", name, f.Text, want)
		}
	}
	// Through the prober: a missing marker fills the name into the vars.
	sp, home := aliasWorld(t)
	dir := filepath.Join(home, "stores", "me")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := sp.Probe(context.Background(), []opssetup.ProbeRequest{{Check: onboard.Check{ID: onboard.CheckStoreMarker, Level: onboard.LevelWarn},
		Answers: onboard.Answers{onboard.QIDStoreURL: {Text: fileuri.URL(dir, "")}}}})[0]
	if r.Status == onboard.StatusOK || r.Vars[onboard.VarStoreName] != "me" {
		t.Fatalf("store.marker = %+v, want a missing marker with store_name me", r)
	}
}

// The checks this prober does not run skip with user words: no internal
// names (op, wire, prober), and a command where one shows the same thing
// (B4-postmerge: doctor --all --json showed "checked by its own op, not by
// the wire prober").
func TestNotHereSkipIsUserWording(t *testing.T) {
	t.Parallel()
	sp, _ := aliasWorld(t)
	for _, tc := range []struct {
		id   onboard.CheckID
		want string
	}{
		// identity.roster and identity.period are run now
		// (TestDoctorAllReadsRosterAndPeriod); with no org they skip.
		{onboard.CheckIdentityRoster, skipNoOrg},
		{onboard.CheckIdentityPeriod, skipNoOrg},
		{onboard.CheckInvitePending, "lore team invite list"},
		{onboard.CheckLiveMode, "not checked here"},
	} {
		r := sp.Probe(context.Background(), []opssetup.ProbeRequest{{Check: onboard.Check{ID: tc.id, Level: onboard.LevelWarn}}})[0]
		if r.Status != onboard.StatusSkip || !strings.Contains(r.Detail, tc.want) {
			t.Errorf("%s = %+v, want a skip naming %q", tc.id, r, tc.want)
		}
		for _, internal := range []string{" op", "wire", "prober"} {
			if strings.Contains(r.Detail, internal) {
				t.Errorf("%s detail %q names %q", tc.id, r.Detail, internal)
			}
		}
	}
}

// A member or admin setup keeps its identity in its org entry (ONB 3.1
// CFG9), so identity.email_format and identity.git_email check that
// entry's address instead of skipping with "no email answered yet"
// (BUGFIX-21); an org entry with no address still skips.
func TestOrgEntryIdentityEmailChecked(t *testing.T) {
	t.Parallel()
	sp, home := aliasWorld(t)
	loreHome := t.TempDir()
	sp.a.opts.Home = loreHome
	cfg := "orgs:\n  - name: agency\n    identity:\n      key: human:tomas@agency.example\n  - name: nomail\n    identity:\n      key: human:tomas\n"
	if err := os.WriteFile(filepath.Join(loreHome, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	gitcfg := func(email string) {
		if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\temail = "+email+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(id onboard.CheckID, org string) opssetup.ProbeResult {
		e := onboard.SetupEntry{Name: "agency-admin", Kind: onboard.KindAdmin, Org: org}
		return sp.Probe(context.Background(), []opssetup.ProbeRequest{{Check: onboard.Check{ID: id, Level: onboard.LevelWarn}, Setup: e}})[0]
	}
	if r := run(onboard.CheckIdentityEmailFormat, "agency"); r.Status != onboard.StatusOK || r.Vars[onboard.VarEmail] != "tomas@agency.example" {
		t.Errorf("email_format = %+v, want ok for the org entry's email", r)
	}
	gitcfg("tomas@agency.example")
	if r := run(onboard.CheckIdentityGitEmail, "agency"); r.Status != onboard.StatusOK {
		t.Errorf("git_email (same) = %+v, want ok", r)
	}
	gitcfg("tomas@personal.example")
	if r := run(onboard.CheckIdentityGitEmail, "agency"); r.Status == onboard.StatusOK || r.Status == onboard.StatusSkip || r.Vars[onboard.VarEmail] != "tomas@agency.example" {
		t.Errorf("git_email (other) = %+v, want a mismatch naming the org entry's email", r)
	}
	for _, id := range []onboard.CheckID{onboard.CheckIdentityEmailFormat, onboard.CheckIdentityGitEmail} {
		if r := run(id, "nomail"); r.Status != onboard.StatusSkip {
			t.Errorf("%s with an org identity without an address = %+v, want skip", id, r)
		}
	}
}

// github.identity_email of a member or admin setup uses its org entry's
// identity email too (same rule as TestOrgEntryIdentityEmailChecked): no
// "no identity email yet" skip when the org entry holds one. No GitHub
// user here, so no gh call: the check stops at "no GitHub account yet"
// with the org entry's email in its vars.
func TestOrgEntryIdentityEmailGitHub(t *testing.T) {
	t.Parallel()
	sp, _ := aliasWorld(t)
	loreHome := t.TempDir()
	sp.a.opts.Home = loreHome
	cfg := "orgs:\n  - name: agency\n    identity:\n      key: human:tomas@agency.example\n"
	if err := os.WriteFile(filepath.Join(loreHome, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	// A route but no user: with no github block at all the github step was
	// skipped and every github check skips before reading the identity.
	e := onboard.SetupEntry{Name: "agency-admin", Kind: onboard.KindAdmin, Org: "agency", GitHub: onboard.SetupGitHub{HostAlias: "github-agency"}}
	r := sp.Probe(context.Background(), []opssetup.ProbeRequest{{Check: onboard.Check{ID: onboard.CheckGitHubIdentityEmail, Level: onboard.LevelWarn}, Setup: e}})[0]
	if r.Vars[onboard.VarEmail] != "tomas@agency.example" || strings.Contains(r.Detail, "no identity email") {
		t.Fatalf("github.identity_email = %+v, want the org entry's email used", r)
	}
}
