package wire

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// setupsBase is a config.yaml with keys lore does not read (a comment, an
// unknown top-level key, an unknown key inside a setup) that every write
// must keep (ONB 3.1).
const setupsBase = `# my machine
orgs:
  - name: agency
    identity: {key: "human:mira@agency.example"}
future_key: {keep: me}
setups:
  - name: personal
    kind: solo
    default: true
    folders: [~/www/mira/**]
    colour: blue   # unknown to lore
`

func writeSetupsConfig(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func readSetupsFile(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSetupsRoundTrip: read with defaults, add, replace and remove an
// entry; unknown keys and comments survive every write.
func TestSetupsRoundTrip(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, setupsBase)
	c, err := ValidateSetupsConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Setups) != 1 || c.Setups[0].Name != "personal" || !c.Setups[0].Default || c.Settings != onboard.DefaultSettings() {
		t.Fatalf("read: %+v", c)
	}
	created, err := PutSetup(home, onboard.SetupEntry{Name: "agency", Kind: onboard.KindMember, Org: "agency",
		Folders: []string{"~/www/agency/**"}, GitHub: onboard.SetupGitHub{User: "mira-agency", HostAlias: "github-agency", SSHKey: "~/.ssh/id_agency.pub"},
		Live: onboard.SetupLive{Autopush: onboard.OptTrue}})
	if err != nil || !created {
		t.Fatalf("add: %v %v", created, err)
	}
	// Replace personal: a known key changes, the unknown one stays.
	if created, err = PutSetup(home, onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Default: true,
		Folders: []string{"~/www/mira/**", "~/notes/**"}}); err != nil || created {
		t.Fatalf("replace: %v %v", created, err)
	}
	if err = SetRepoSetup(home, "rpo_1", "agency"); err != nil {
		t.Fatal(err)
	}
	got := readSetupsFile(t, home)
	for _, keep := range []string{"# my machine", "future_key:", "keep: me", "colour: blue", "# unknown to lore", "autopush: true", "~/notes/**", "rpo_1: agency"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q:\n%s", keep, got)
		}
	}
	c, err = ValidateSetupsConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := c.Setup("agency"); !ok || e.Live.Autopush != onboard.OptTrue || e.GitHub.HostAlias != "github-agency" {
		t.Errorf("agency read back: %+v", e)
	}
	found, err := RemoveSetup(home, "agency")
	if err != nil || !found {
		t.Fatalf("remove: %v %v", found, err)
	}
	got = readSetupsFile(t, home)
	if strings.Contains(got, "agency/**") || strings.Contains(got, "rpo_1") {
		t.Errorf("remove left the entry or its pin:\n%s", got)
	}
	if found, _ = RemoveSetup(home, "agency"); found {
		t.Error("second remove found it again")
	}
	if _, err = SetupByName(home, "nope"); !errs.Has(err, errs.SetupNotFound) {
		t.Errorf("unknown setup: %v", err)
	}
}

// TestSetupsRules: every 3.2 / 7.3 rule refuses with E_CONFIG_INVALID
// naming the entry, and a refused write leaves the file untouched.
func TestSetupsRules(t *testing.T) {
	t.Parallel()
	head := "orgs:\n  - name: agency\nsetups:\n"
	cases := map[string]struct{ body, want string }{
		"name format":     {"  - {name: Bad Name, kind: solo}\n", "setups[Bad Name]: name"},
		"name missing":    {"  - {kind: solo}\n", "setups[0]: name is required"},
		"name twice":      {"  - {name: a, kind: solo}\n  - {name: a, kind: solo}\n", "setups[a]: name is used by two setups"},
		"kind":            {"  - {name: a, kind: boss}\n", "setups[a]: kind"},
		"two defaults":    {"  - {name: a, kind: solo, default: true}\n  - {name: b, kind: solo, default: true}\n", "at most one setup may be the default"},
		"org required":    {"  - {name: a, kind: member}\n", "setups[a]: org is required"},
		"org unknown":     {"  - {name: a, kind: admin, org: acme-hub}\n", "setups[a]: org \"acme-hub\" is not an orgs[] entry"},
		"identity solo":   {"  - {name: a, kind: member, org: agency, identity: {key: \"human:tomas@agency.example\"}}\n", "setups[a]: identity is for solo setups only"},
		"folder relative": {"  - {name: a, kind: solo, folders: [www/**]}\n", "setups[a]: folder \"www/**\" must be absolute"},
		"key relative":    {"  - {name: a, kind: solo, github: {ssh_key: id_ed25519}}\n", "setups[a]: github.ssh_key"},
		"route unknown":   {"  - {name: a, kind: solo, github: {user: octo-user, route: ftp}}\n", "setups[a]: github.route"},
		"https no user":   {"  - {name: a, kind: solo, github: {route: https}}\n", "github.route https needs github.user"},
		"tool unknown":    {"  - {name: a, kind: solo, tools: [vim]}\n", "setups[a]: tool \"vim\""},
		"autopush":        {"  - {name: a, kind: solo, live: {autopush: maybe}}\n", "setups[a]: live.autopush"},
		"pin":             {"  - {name: a, kind: solo}\nrepo_setups: {rpo_1: b}\n", "repo_setups[rpo_1]: no setup named \"b\""},
		"idle":            {"  - {name: a, kind: solo}\nsetup: {idle_minutes: 0}\n", "setup.idle_minutes 0"},
		"scan depth":      {"  - {name: a, kind: solo}\ndoctor: {scan_depth: 99}\n", "doctor.scan_depth 99"},
		"probe timeout":   {"  - {name: a, kind: solo}\ndoctor: {probe_timeout_s: -1}\n", "doctor.probe_timeout_s -1"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			home := writeSetupsConfig(t, head+c.body)
			_, err := ValidateSetupsConfig(home)
			var pe *errs.Error
			if !errors.As(err, &pe) || pe.Code != errs.ConfigInvalid || !strings.Contains(pe.Message, c.want) {
				t.Fatalf("got %v, want E_CONFIG_INVALID with %q", err, c.want)
			}
		})
	}
	// A write that would break a rule writes nothing.
	home := writeSetupsConfig(t, setupsBase)
	before := readSetupsFile(t, home)
	if _, err := PutSetup(home, onboard.SetupEntry{Name: "second", Kind: onboard.KindSolo, GitHub: onboard.SetupGitHub{Route: "carrier-pigeon"}}); !errs.Has(err, errs.ConfigInvalid) {
		t.Fatalf("bad route: %v", err)
	}
	if readSetupsFile(t, home) != before {
		t.Error("a refused write changed config.yaml")
	}
}

// TestSetupsConfigCheck: lore config check reports a broken setup and
// does not warn about the onboarding keys as unknown.
func TestSetupsConfigCheck(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, "setups:\n  - {name: a, kind: solo}\nrepo_setups: {rpo_1: a}\nsetup: {idle_minutes: 5, open_browser: false}\ndoctor: {scan_depth: 2, probe_timeout_s: 30}\n")
	out, err := standaloneConfig{home: home}.CheckConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range out.Warnings {
		if strings.Contains(w, "setup") || strings.Contains(w, "doctor") {
			t.Errorf("onboarding key warned as unknown: %s", w)
		}
	}
	c, _ := ReadSetupsConfig(home)
	if c.Settings != (onboard.Settings{IdleMinutes: 5, OpenBrowser: false, ScanDepth: 2, ProbeTimeoutS: 30}) {
		t.Errorf("settings: %+v", c.Settings)
	}
	home = writeSetupsConfig(t, "setups:\n  - {name: a, kind: member}\n")
	if _, err = (standaloneConfig{home: home}).CheckConfig(t.Context()); !errs.Has(err, errs.ConfigInvalid) || !strings.Contains(err.Error(), "setups[a]") {
		t.Errorf("check of a broken setup: %v", err)
	}
}

// TestSetupsTildeIsUserHome: ~ in a folder glob is the user's home, not
// the lore home (a lore home elsewhere must not change what ~/ means).
func TestSetupsTildeIsUserHome(t *testing.T) {
	user := t.TempDir()
	testenv.SetHome(t, user)
	loreHome := writeSetupsConfig(t, "setups:\n  - {name: a, kind: solo, folders: [~/www/**]}\n")
	if _, err := ValidateSetupsConfig(loreHome); err != nil {
		t.Fatalf("~/ glob refused: %v", err)
	}
	if got := globHome(); got != user {
		t.Fatalf("globHome = %q, want the user home %q", got, user)
	}
	if strings.HasPrefix(onboard.ExpandHome("~/www/**", globHome()), loreHome) {
		t.Fatal("~ expanded under the lore home")
	}
}

// Saved github blocks of every shape load and validate: a config saved
// before 2026-11-07 (a .pub path, no route), the ssh route with a private
// key path and fingerprint (no .pub), the alias alone with a fingerprint,
// and the https route.
func TestSetupsGitHubShapesLoad(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"old .pub":    "  - {name: a, kind: solo, github: {user: octo-user, host_alias: octo-gh, ssh_key: ~/.ssh/id_octo.pub}}\n",
		"private key": "  - {name: a, kind: solo, github: {user: octo-user, route: ssh, host_alias: github.com, ssh_key: ~/.ssh/main-key, key_fingerprint: \"SHA256:abc\"}}\n",
		"alias only":  "  - {name: a, kind: solo, github: {user: octo-user, route: ssh, host_alias: github.com, key_fingerprint: \"SHA256:abc\"}}\n",
		"https":       "  - {name: a, kind: solo, github: {user: octo-user, route: https}}\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := writeSetupsConfig(t, "setups:\n"+body)
			if _, err := ValidateSetupsConfig(home); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestPutSetupsOneDefaultAgainstTheFile: a default entry written after a
// read that saw no default (two first setups saved at once, each told "no
// setup is the default yet") clears the default another process wrote in
// between, instead of failing the save with two defaults (2026-11-08,
// TestCampaignSetup_ConcurrentFirstUse under load).
func TestPutSetupsOneDefaultAgainstTheFile(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, "setups:\n  - name: first-a\n    kind: solo\n    default: true\n")
	if err := putSetups(home, []onboard.SetupEntry{{Name: "first-b", Kind: onboard.KindSolo, Default: true}}); err != nil {
		t.Fatalf("second default save: %v", err)
	}
	cfg, err := ReadSetupsConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	var defaults []string
	for _, s := range cfg.Setups {
		if s.Default {
			defaults = append(defaults, s.Name)
		}
	}
	if len(cfg.Setups) != 2 || len(defaults) != 1 || defaults[0] != "first-b" {
		t.Errorf("setups %v, defaults %v; want both kept and first-b the default", cfg.Setups, defaults)
	}
}
