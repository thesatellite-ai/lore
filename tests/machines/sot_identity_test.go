//go:build machines && !windows

package machines

// sot_identity_test.go — SOURCE_OF_TRUTH row 1 (person identity in
// config.yaml) through the built binary and `lore api`, on the owner's real
// case with placeholders: the top-level identity says Pat One, the default
// solo setup says Pat Two, one key.
//
//	two names ─► whoami/identity show: the setup answers, named as the source
//	          ─► doctor (CLI) and doctor --all (REST): two fixes, one per value
//	          ─► identity migrate without --keep: refused, file untouched
//	Run it (REST setup.fix, --keep setup) ─► no top-level block, setup keeps Pat Two
//	identity set "Pat Three <…>" ─► the setup changes; whoami and REST show follow

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

func TestSotIdentity(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	repo := m.Repos[RepoLore]
	cfg := filepath.Join(m.Home, ".lore", "config.yaml")
	sotiWrite(t, cfg, sotiTopPatOne+sotiSetupPatTwo)

	if out := sotiRun(t, m, repo, 0, "identity", "show"); !strings.Contains(out, "display:     Pat Two") || !strings.Contains(out, "setups[personal].identity") {
		t.Errorf("identity show with two names: %s", out)
	}
	doc := sotiRun(t, m, repo, -1, "doctor")
	for _, w := range []string{"identity_source", "never picks", "lore identity migrate --keep setup --setup personal", "lore identity migrate --keep top --setup personal"} {
		if !strings.Contains(doc, w) {
			t.Errorf("lore doctor lacks %q:\n%s", w, doc)
		}
	}

	api := csStartAPI(t, m, repo)
	checks := sotiDoctorAll(t, api)
	for id, keep := range map[onboard.CheckID]string{onboard.CheckIdentityTopLevel: "setup", onboard.CheckIdentityTopLevelKeep: "top"} {
		c, ok := checks[id]
		want := "lore identity migrate --keep " + keep + " --setup personal"
		if !ok || (c.Status != onboard.StatusFail && c.Status != onboard.StatusWarn) || !c.Fix.Runnable || strings.Join(c.Fix.Command, " ") != want {
			t.Errorf("REST doctor --all %s = %+v, want a warning whose Run it is %q", id, c, want)
		}
	}

	before := sotiRead(t, cfg)
	if st, body := api.call(t, http.MethodPost, "/v1/identities/migrate", `{}`); st != http.StatusConflict || !strings.Contains(body, "E_REFUSED") || !strings.Contains(body, "Pat One") || !strings.Contains(body, "Pat Two") {
		t.Errorf("REST identity migrate without keep: HTTP %d %s; want E_REFUSED naming both", st, body)
	}
	if sotiRead(t, cfg) != before {
		t.Fatalf("a refused migrate changed config.yaml:\n%s", sotiRead(t, cfg))
	}

	// Run it: keep the setup's name.
	fix := sotAPICall(t, api, http.MethodPost, "/v1/setup/fix", `{"setup":"personal","check":"identity.top_level","yes":true}`, sotStatusOK)
	var fx struct {
		Exit    int    `json:"exit"`
		Recheck string `json:"recheck"`
	}
	if err := json.Unmarshal([]byte(fix), &fx); err != nil || fx.Exit != 0 || fx.Recheck != string(onboard.StatusOK) {
		t.Fatalf("Run it on identity.top_level: %s (%v)", fix, err)
	}
	if got := sotiRead(t, cfg); strings.HasPrefix(got, "identity:") || !strings.Contains(got, "name: Pat Two") || strings.Contains(got, "Pat One") {
		t.Fatalf("after keep setup, config.yaml:\n%s", got)
	}

	// Rename: identity set writes the setup, whoami and REST follow.
	if out := sotiRun(t, m, repo, 0, "identity", "set", "Pat Three <pat@example.com>"); !strings.Contains(out, "setup personal") {
		t.Errorf("identity set: %s", out)
	}
	if got := sotiRead(t, cfg); strings.HasPrefix(got, "identity:") || !strings.Contains(got, "name: Pat Three") {
		t.Errorf("after identity set, config.yaml:\n%s", got)
	}
	var who struct {
		Data struct {
			Identity struct {
				Name   string `json:"name"`
				Source string `json:"source"`
			} `json:"identity"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(sotiRun(t, m, repo, 0, "whoami", "--json")), &who); err != nil || who.Data.Identity.Name != "Pat Three" ||
		!strings.Contains(who.Data.Identity.Source, "setups[personal]") {
		t.Errorf("whoami after identity set: %+v (%v)", who, err)
	}
	show := sotAPICall(t, api, http.MethodGet, "/v1/identities/show", "", sotStatusOK)
	if !strings.Contains(show, `"display_name":"Pat Three"`) || !strings.Contains(show, "setups[personal].identity") {
		t.Errorf("REST identity show after identity set: %s", show)
	}
	if c := sotiDoctorAll(t, api)[onboard.CheckIdentityTopLevel]; c.Status != onboard.StatusOK {
		t.Errorf("REST doctor --all after the fix: %+v", c)
	}
}

// Placeholder config.yaml blocks of the owner's case.
const (
	sotiTopPatOne   = "identity:\n  key: human:pat@example.com\n  name: Pat One\n"
	sotiSetupPatTwo = "setups:\n  - name: personal\n    kind: solo\n    default: true\n    identity:\n      key: human:pat@example.com\n      name: Pat Two\n"
)

// sotiWrite writes a file (mode 0600).
func sotiWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// sotiRead reads a file.
func sotiRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sotiRun runs lore in cwd; want is the exit code (-1: any) and the
// output is stdout and stderr.
func sotiRun(t *testing.T, m *Machine, cwd string, want int, args ...string) string {
	t.Helper()
	r := m.Lore(t, cwd, args...)
	if want >= 0 && r.Code != want {
		t.Fatalf("%s", r)
	}
	return r.Stdout + r.Stderr
}

// sotiDoctorAll runs doctor --all for setup personal over REST and returns
// its checks by id.
func sotiDoctorAll(t *testing.T, api *csAPI) map[onboard.CheckID]onboard.CheckResult {
	t.Helper()
	body := sotAPICall(t, api, http.MethodPost, "/v1/doctor/run", `{"all":true,"fresh":true,"setup":"personal"}`, sotStatusOK)
	var rep opsdoctor.Report
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("doctor --all report: %v: %s", err, body)
	}
	out := map[onboard.CheckID]onboard.CheckResult{}
	for _, s := range rep.Setups {
		for _, c := range s.Checks {
			out[c.Check] = c
		}
	}
	return out
}

// sotRetireTopIdentity is what a person upgrading from v0.1.17 does when
// the shape still holds the old top-level identity: equal to setup's own
// (SOURCE_OF_TRUTH row 1, approved): doctor --all flags the leftover with
// its Run it fix, the fix runs, and doctor --all no longer mentions it.
// cwd is where doctor and the fix run ("" = home).
func sotRetireTopIdentity(t *testing.T, m *Machine, cwd, setup string) {
	t.Helper()
	fix := "lore identity migrate --keep setup --setup " + setup
	if d := m.Lore(t, cwd, "doctor", "--all"); !strings.Contains(d.Stdout, "identity.top_level") || !strings.Contains(d.Stdout, fix) {
		t.Errorf("doctor --all does not flag the leftover top-level identity: with %q: %s", fix, d)
	}
	sotiRun(t, m, cwd, 0, "identity", "migrate", "--keep", "setup", "--setup", setup)
	if d := m.Lore(t, cwd, "doctor", "--all"); strings.Contains(d.Stdout, "identity.top_level") || strings.Contains(d.Stdout, "identity migrate") {
		t.Errorf("doctor --all still flags the top-level identity: after %s: %s", fix, d)
	}
}
