package wire_test

// OWN7-5 (owner 2026-11-10): the first unchanged GitHub save fills route
// and key_fingerprint on an entry saved before they were recorded (a
// config written by an earlier lore: github {user, host_alias} only).
// Before, `lore setup edit <name> --github-user <same>` compared the
// edited entry with the old one, found them equal and wrote nothing, so
// the old entry never got a fingerprint and github.key_matches had
// nothing to compare. Later unchanged saves stay no-ops, and a recorded
// fingerprint is never refreshed by one (it is what detects a swapped key).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/wire"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// oldGitHubEntry writes one finished solo setup whose github block has
// the given extra lines (an earlier lore's shape) and returns config.yaml.
func oldGitHubEntry(t *testing.T, h *opstest.H, extra string) string {
	t.Helper()
	path := filepath.Join(h.Home, wire.ConfigFile)
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	body := string(old) + "setups:\n  - name: personal\n    kind: solo\n    default: true\n    github:\n      user: octo-user\n      host_alias: github.com\n" + extra
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func savedGitHub(t *testing.T, h *opstest.H) onboard.SetupGitHub {
	t.Helper()
	cfg, err := wire.ReadSetupsConfig(h.Home)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := cfg.Setup("personal")
	if !ok {
		t.Fatal("setup personal is gone")
	}
	return e.GitHub
}

func TestFirstUnchangedGitHubSaveFillsRouteAndFingerprint(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	h := realSSHWorld(t)
	path := oldGitHubEntry(t, h, "")
	const edit = `{"name":"personal","github_user":"octo-user","host_alias":"github.com"}`

	var out opssetup.EditOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opssetup.OpEdit, edit))), &out); err != nil {
		t.Fatal(err)
	}
	g := savedGitHub(t, h)
	if !out.Changed || g.Route != onboard.RouteSSH || !strings.HasPrefix(g.KeyFingerprint, "SHA256:") {
		t.Fatalf("first unchanged save: changed=%v github=%+v; want changed, route ssh and the key's fingerprint", out.Changed, g)
	}
	if g.User != "octo-user" || g.HostAlias != "github.com" || g.SSHKey != "" {
		t.Fatalf("the save changed what was given: %+v", g)
	}
	if _, err := wire.ValidateSetupsConfig(h.Home); err != nil {
		t.Fatalf("filled entry does not validate: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// The second unchanged save is a no-op: nothing written.
	out = opssetup.EditOut{}
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opssetup.OpEdit, edit))), &out); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.Changed || string(after) != string(before) {
		t.Fatalf("second unchanged save changed=%v, config.yaml rewritten: %v", out.Changed, string(after) != string(before))
	}

	// Doctor's key_matches now has the recorded fingerprint and it matches.
	cfg, err := wire.ReadSetupsConfig(h.Home)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := cfg.Setup("personal")
	pr, err := h.App.SetupProber(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	km := pr.Probe(t.Context(), []opssetup.ProbeRequest{{Check: onboard.Check{ID: onboard.CheckGitHubKeyMatches, Level: onboard.LevelError}, Setup: e}})[0]
	if km.Status != onboard.StatusOK {
		t.Fatalf("key_matches after the fill = %s %q", km.Status, km.Detail)
	}
}

// A recorded fingerprint is kept by an unchanged save (only the missing
// route is filled): refreshing it would hide a key swapped since.
func TestUnchangedGitHubSaveKeepsARecordedFingerprint(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	h := realSSHWorld(t)
	oldGitHubEntry(t, h, "      key_fingerprint: \"SHA256:recorded-earlier\"\n")
	var out opssetup.EditOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opssetup.OpEdit, `{"name":"personal","github_user":"octo-user"}`))), &out); err != nil {
		t.Fatal(err)
	}
	g := savedGitHub(t, h)
	if !out.Changed || g.Route != onboard.RouteSSH || g.KeyFingerprint != "SHA256:recorded-earlier" {
		t.Fatalf("changed=%v github=%+v; want route filled and the recorded fingerprint kept", out.Changed, g)
	}
}

// An edit that gives no github flag (the note) leaves the old block as it
// is: only a GitHub save records route and fingerprint.
func TestNoteEditLeavesTheGitHubBlock(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	h := realSSHWorld(t)
	oldGitHubEntry(t, h, "")
	h.Must(opssetup.OpEdit, `{"name":"personal","note":"work laptop"}`)
	if g := savedGitHub(t, h); g.Route != "" || g.KeyFingerprint != "" {
		t.Fatalf("a note edit recorded github fields: %+v", g)
	}
}

// The same key path given again (setup edit --ssh-key <same>) is an
// unchanged save too: the recorded fingerprint stays (before, any
// --ssh-key cleared it and the save recorded the key there now, so a key
// swapped behind the same path went unseen by github.key_matches).
func TestSameSSHKeyAgainKeepsTheRecordedFingerprint(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	h := realSSHWorld(t)
	path := oldGitHubEntry(t, h, "      ssh_key: ~/.ssh/main-key\n      route: ssh\n      key_fingerprint: \"SHA256:recorded-earlier\"\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out opssetup.EditOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opssetup.OpEdit, `{"name":"personal","ssh_key":"~/.ssh/main-key"}`))), &out); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if g := savedGitHub(t, h); out.Changed || g.KeyFingerprint != "SHA256:recorded-earlier" || string(after) != string(before) {
		t.Fatalf("changed=%v github=%+v; want nothing written and the recorded fingerprint kept", out.Changed, g)
	}
	// Another key is a change: its fingerprint is recorded.
	h.Must(opssetup.OpEdit, `{"name":"personal","ssh_key":"~/.ssh/work-key"}`)
	if g := savedGitHub(t, h); g.SSHKey != "~/.ssh/work-key" || g.KeyFingerprint == "SHA256:recorded-earlier" || !strings.HasPrefix(g.KeyFingerprint, "SHA256:") {
		t.Fatalf("after a new key: %+v, want its fingerprint recorded", g)
	}
}
