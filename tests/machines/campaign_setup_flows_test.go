//go:build machines && !windows

package machines

// campaign_setup_flows_test.go — the setup area's multi-step flows on
// realistic machines: machine profiles in a personal store, importing an
// old install, step check/repair/fix, the page commands, the GitHub
// accounts of an owner-like machine, and REST writes read back by the CLI.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestCampaignSetup_MachineProfiles: a label needs an own store (an empty
// repo is refused with the next step); once the personal store exists the
// label round-trips odd values, a second run changes nothing, and show
// finds the profile by its full id and by a prefix.
func TestCampaignSetup_MachineProfiles(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	repo := m.Repos[RepoLore]
	// A plain empty folder: a file:// store is a folder store, and a bare
	// git repository given as one is refused (store init, batch fsy).
	bare := filepath.Join(m.Dir, "me-store")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	csOK(t, m.Lore(t, repo, "identity", "set", PersonEmail, "--json"))
	// A personal store url naming an empty folder: every repo command says
	// how to make it a store.
	csOK(t, m.Lore(t, repo, "config", "set", "personal.store", "file://"+bare))
	csRefused(t, m.Lore(t, repo, "machine", "list", "--json"), "E_NOT_A_STORE")
	csRefused(t, m.Lore(t, repo, "machine", "label", "laptop", "--json"), "E_NOT_A_STORE")
	csOK(t, m.Lore(t, repo, "store", "init", "me", "file://"+bare, "--yes"))

	var id string
	for _, label := range []string{"work laptop 🙂", "מחשב עבודה", strings.Repeat("x", 10000), ""} {
		for i := range 2 {
			r := m.Lore(t, repo, "machine", "label", label, "--json")
			var o struct {
				ID      string `json:"id"`
				Label   string `json:"label"`
				Changed bool   `json:"changed"`
			}
			if err := json.Unmarshal([]byte(csOK(t, r)), &o); err != nil {
				t.Fatal(err)
			}
			csConforms(t, "machine.label", r.Stdout)
			if o.Label != label || o.Changed != (i == 0) {
				t.Errorf("label %.20q run %d: %s", label, i+1, r.Stdout)
			}
			id = o.ID
		}
		list := csOK(t, m.Lore(t, repo, "machine", "list", "--json"))
		csConforms(t, "machine.list", list)
		lb, _ := json.Marshal(label)
		if label != "" && !strings.Contains(list, `"label": `+string(lb)) && !strings.Contains(list, `"label":`+string(lb)) {
			t.Errorf("list lacks label %.20q", label)
		}
	}
	for _, q := range []string{id, id[:12]} {
		r := m.Lore(t, repo, "machine", "show", q, "--json")
		csConforms(t, "machine.show", csOK(t, r))
		if !strings.Contains(r.Stdout, id) {
			t.Errorf("show %s: %s", q, r)
		}
	}
	csRefused(t, m.Lore(t, repo, "machine", "show", "mch_00000000", "--json"), "E_NOT_FOUND")
	csRefused(t, m.Lore(t, repo, "machine", "label", "two\nlines", "--json"), "E_INVALID_INPUT")
}

// TestCampaignSetup_ImportOldInstall: import-scan's proposals import, a
// second import skips them by name, and import-dismiss is idempotent.
func TestCampaignSetup_ImportOldInstall(t *testing.T) {
	t.Parallel()
	// An old install proposes its identity as a setup; machines whose
	// config.yaml already has setups (or backups only) propose nothing.
	for shape, wantProposal := range map[string]bool{ShapeV0117: true, ShapeOrgMember: false, ShapeMigrated: false, ShapeGraveyard: false} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			m := Build(t, shape, loreBin)
			scan := csOK(t, m.Lore(t, "", "setup", "import-scan", "--json"))
			var sc struct {
				Proposals []struct {
					Setup json.RawMessage `json:"setup"`
				} `json:"proposals"`
			}
			if err := json.Unmarshal([]byte(scan), &sc); err != nil {
				t.Fatal(err)
			}
			csConforms(t, "setup.import_scan", scan)
			if (len(sc.Proposals) > 0) != wantProposal {
				t.Fatalf("import-scan proposals %d, want some=%v: %s", len(sc.Proposals), wantProposal, scan)
			}
			if !wantProposal {
				return
			}
			entries := make([]json.RawMessage, 0, len(sc.Proposals))
			for _, p := range sc.Proposals {
				entries = append(entries, p.Setup)
			}
			list, _ := json.Marshal(entries)
			var first, second struct {
				Imported []string `json:"imported"`
				Skipped  []struct {
					Name string `json:"name"`
				} `json:"skipped"`
			}
			r := m.Lore(t, "", "setup", "import", "--setups", string(list), "--json")
			csConforms(t, "setup.import", csOK(t, r))
			_ = json.Unmarshal([]byte(r.Stdout), &first)
			r = m.Lore(t, "", "setup", "import", "--setups", string(list), "--json")
			_ = json.Unmarshal([]byte(csOK(t, r)), &second)
			if len(second.Imported) != 0 || len(second.Skipped) != len(first.Imported)+len(first.Skipped) {
				t.Errorf("second import: %s (first imported %v)", r.Stdout, first.Imported)
			}
			names := map[string]bool{}
			for _, s := range m.List(t, "").Setups {
				names[s.Name] = true
			}
			for _, n := range first.Imported {
				if !names[n] {
					t.Errorf("imported %s is not listed", n)
				}
			}
			// Once imported, the old install is covered: nothing to propose.
			again := csOK(t, m.Lore(t, "", "setup", "import-scan", "--json"))
			var sc2 struct {
				Proposals []json.RawMessage `json:"proposals"`
			}
			if json.Unmarshal([]byte(again), &sc2) != nil || len(sc2.Proposals) != 0 {
				t.Errorf("import-scan proposes the imported setup again: %.300s", again)
			}
			for range 2 {
				csOK(t, m.Lore(t, "", "setup", "import-dismiss", "0123456789abcdef", "--json"))
			}
			m.CheckMachine(t, "import", Gone{})
		})
	}
}

// TestCampaignSetup_StepChecks: check validates without saving the entry,
// apply then saves it, repair --dry-run and repair list the same steps,
// and fix refuses an unknown check or a passing one with the next step.
func TestCampaignSetup_StepChecks(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeOwnerLike, loreBin)
	csOK(t, m.Lore(t, "", "setup", "apply", "acct", "--step", "kind", "--answers", csKindAnswers("acct"), "--json"))
	cases := []struct {
		answers string
		status  string
	}{
		{`{"name":"` + PersonName + `","email":"` + PersonEmail + `"}`, "ready"},
		{`{"name":"Zoë 🙂","email":"zoe@example.test","emails":["a@example.test","b@example.test"]}`, "ready"},
		{`{"name":"` + PersonName + `","email":"not-an-email"}`, ""},
		{`{"name":"","email":"` + PersonEmail + `"}`, ""},
	}
	for _, c := range cases {
		before := csConfig(t, m)
		r := m.Lore(t, "", "setup", "check", "acct", "--step", "identity", "--answers", c.answers, "--json")
		out := csOK(t, r)
		csConforms(t, "setup.check", out)
		var o struct {
			Status      string            `json:"status"`
			FieldErrors []json.RawMessage `json:"field_errors"`
		}
		_ = json.Unmarshal([]byte(out), &o)
		if c.status != "" && o.Status != c.status {
			t.Errorf("check %s: status %q: %s", c.answers, o.Status, out)
		}
		if c.status == "" && len(o.FieldErrors) == 0 {
			t.Errorf("check %s: no field error: %s", c.answers, out)
		}
		if csConfig(t, m) != before {
			t.Errorf("setup check wrote config.yaml")
		}
	}
	// The GitHub step on the owner-like machine: bravo's alias logs in as
	// bravo.
	r := m.Lore(t, "", "setup", "check", "acct", "--step", "github", "--answers", `{"github_account":"`+LoginBravo+`","host_alias":"github-bravo"}`, "--json")
	csConforms(t, "setup.check", csOK(t, r))
	home := csHash(t, filepath.Join(m.Home, ".lore"), "machine.db", "machine.db-wal", "machine.db-shm", "state", "cache")
	dry := csOK(t, m.Lore(t, "", "setup", "repair", "acct", "--dry-run", "--json"))
	csConforms(t, "setup.repair", dry)
	if csHash(t, filepath.Join(m.Home, ".lore"), "machine.db", "machine.db-wal", "machine.db-shm", "state", "cache") != home {
		t.Errorf("repair --dry-run changed ~/.lore")
	}
	csRefused(t, m.Lore(t, "", "setup", "fix", "acct", "--check", "no.such_check", "--yes", "--json"), "E_INVALID_INPUT")
	csRefused(t, m.Lore(t, "", "setup", "fix", "nope", "--check", "tools.pointer_file", "--yes", "--json"), "E_SETUP_NOT_FOUND")
}

// TestCampaignSetup_MoreRefusals: refusals outside the setup noun name
// their next step too.
func TestCampaignSetup_MoreRefusals(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	cases := []struct {
		name, cwd string
		args      []string
		code      string
	}{
		{"compile no sources", "", []string{"skill", "compile", "--source-dir", filepath.Join(m.Dir, "nothing"), "--dry-run"}, "E_NOT_FOUND"},
		{"agent hooks outside a repo", "", []string{"setup", "--agent-hooks", "--dry-run"}, "E_INVALID_INPUT"},
		{"import not JSON", "", []string{"setup", "import", "--setups", "nope"}, "E_INVALID_INPUT"},
		{"import not a list", "", []string{"setup", "import", "--setups", `{"name":"x"}`}, "E_INVALID_INPUT"},
		{"dismiss empty", "", []string{"setup", "import-dismiss", ""}, "E_INVALID_INPUT"},
		{"skill install unknown tool", "", []string{"skill", "install", "--tool", "nope"}, "E_INVALID_INPUT"},
		{"schema-only outside a repo", "", []string{"setup", "run", "--schema-only"}, "E_NOT_A_STORE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := csRefused(t, m.Lore(t, c.cwd, append(c.args, "--json")...), c.code)
			if e != nil && strings.Contains(e.Message, "["+c.code+"]") {
				t.Errorf("the message repeats its own code: %s", e.Message)
			}
		})
	}
}

var csPageRe = regexp.MustCompile(`lore setup is open at (http://127\.0\.0\.1:[0-9]+/setup/\S*)`)

// TestCampaignSetup_PageCommands: setup web and onboard --web print the
// page's local address with --no-open and serve it; without a terminal
// and without --web they run the plain setup and exit 0.
func TestCampaignSetup_PageCommands(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeMigrated, loreBin)
	repo := m.Repos[RepoLore]
	for _, args := range [][]string{{"setup", "web", "--no-open"}, {"onboard", "--web", "--no-open"}, {"setup", "--web", "--no-open"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := exec.CommandContext(ctx, m.bin, args...)
			// The address line goes to stderr (stdout keeps the op's lines).
			var outBuf strings.Builder
			c.Dir, c.Env, c.Stdout = repo, m.env(), &outBuf
			so, err := c.StderrPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { cancel(); _ = c.Wait() }()
			found := make(chan string, 1)
			var seen []string
			go func() {
				sc := bufio.NewScanner(so)
				for sc.Scan() {
					seen = append(seen, sc.Text())
					if mm := csPageRe.FindStringSubmatch(sc.Text()); mm != nil {
						found <- mm[1]
						_, _ = io.Copy(io.Discard, so)
						return
					}
				}
				found <- ""
			}()
			var url string
			select {
			case url = <-found:
			case <-time.After(30 * time.Second):
			}
			if url == "" {
				cancel()
				_ = c.Wait()
				t.Fatalf("lore %s printed no page address:\nstderr %s\nstdout %s", strings.Join(args, " "), strings.Join(seen, "\n"), outBuf.String())
			}
			resp, err := http.Get(url)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != 200 || !strings.Contains(strings.ToLower(string(body)), "<html") {
				t.Errorf("page %s: %d %.200s", url, resp.StatusCode, body)
			}
		})
	}
	for _, args := range [][]string{{"onboard", "--no-open"}, {"setup", "--no-open"}} {
		r := m.Lore(t, repo, args...)
		if r.Code != 0 || csPageRe.MatchString(r.Stdout+r.Stderr) {
			t.Errorf("lore %s without a terminal: %s", strings.Join(args, " "), r)
		}
	}
}

// TestCampaignSetup_OwnerAccounts: on a machine with three gh logins and
// one alias per account, discover and aliases name every login, each alias
// logs in as its own account, and the alias whose key is gone is named.
func TestCampaignSetup_OwnerAccounts(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeOwnerLike, loreBin)
	disc := csOK(t, m.Lore(t, "", "setup", "discover", "--json"))
	for _, l := range []string{LoginAlpha, LoginBravo, LoginCharlie} {
		if !strings.Contains(disc, l) {
			t.Errorf("discover lacks gh login %s", l)
		}
	}
	for _, extra := range [][]string{nil, {"--keys"}, {"--fresh"}, {"--user", LoginBravo}} {
		r := m.Lore(t, "", append([]string{"setup", "aliases", "--json"}, extra...)...)
		out := csOK(t, r)
		csConforms(t, "setup.aliases", out)
		for _, a := range []string{"github-alpha", "github-bravo", "github-gone"} {
			if !strings.Contains(out, a) {
				t.Errorf("aliases %v lacks %s", extra, a)
			}
		}
		if strings.Contains(out, "build-box") {
			t.Errorf("aliases %v lists a host that is not github.com", extra)
		}
	}
	b, err := os.ReadFile(filepath.Join(m.Home, ".ssh", "config"))
	if err != nil || string(b) != ownerSSHConfig {
		t.Errorf("lore changed ~/.ssh/config")
	}
}

// TestCampaignSetup_RESTWrites: the setup lifecycle driven over REST is
// read back the same by the CLI, and REST refusals carry the CLI's codes.
func TestCampaignSetup_RESTWrites(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeMigrated, loreBin)
	repo := m.Repos[RepoLore]
	api := csStartAPI(t, m, repo)
	post := func(op, body string) (int, string) {
		t.Helper()
		rt := csRouteOf(t, op)
		return api.call(t, rt.Method, rt.Path, body)
	}
	if st, b := post("setup.apply", `{"setup":"viarest","step":"kind","answers":{"kind":"solo","name":"viarest"}}`); st != 200 {
		t.Fatalf("apply kind: %d %s", st, b)
	}
	if st, b := post("setup.apply", `{"setup":"viarest","step":"identity","answers":{"name":"Zoë","email":"zoe@example.test"}}`); st != 200 {
		t.Fatalf("apply identity: %d %s", st, b)
	}
	if s := csOK(t, m.Lore(t, repo, "setup", "show", "viarest", "--json")); !strings.Contains(s, "zoe@example.test") {
		t.Errorf("CLI does not see the REST apply: %s", s)
	}
	if st, b := api.call(t, "PATCH", "/v1/setup/viarest", `{"folders":["~/rest/**"]}`); st != 200 {
		t.Errorf("edit: %d %s", st, b)
	}
	if s := csOK(t, m.Lore(t, repo, "setup", "show", "viarest", "--json")); !strings.Contains(s, "~/rest/**") {
		t.Errorf("CLI does not see the REST edit: %s", s)
	}
	for _, c := range []struct {
		op, body string
		status   int
		code     string
	}{
		{"setup.disable", `{"name":"viarest"}`, 200, ""},
		{"setup.enable", `{"name":"viarest"}`, 200, ""},
		{"setup.remove", `{"name":"viarest"}`, 200, ""},
		{"setup.remove", `{"name":"viarest"}`, 404, "E_SETUP_NOT_FOUND"},
		{"setup.trash.restore", `{"name":"viarest"}`, 200, ""},
		{"setup.apply", `{"setup":"me","step":"kind","answers":{"kind":"solo","name":"me"}}`, 400, "E_INVALID_INPUT"},
		{"setup.apply", `{"setup":"x","step":"kind","answers":"{not json"}`, 400, "E_INVALID_INPUT"},
	} {
		st, b := post(c.op, c.body)
		if st != c.status || (c.code != "" && !strings.Contains(b, c.code)) {
			t.Errorf("%s %s: %d %s, want %d %s", c.op, c.body, st, b, c.status, c.code)
		}
	}
	if l := m.List(t, repo); !csListed(l, "viarest") {
		t.Errorf("after REST restore the CLI does not list the setup: %+v", l)
	}
	// Parity of the machine-wide reads on this machine.
	for _, c := range []struct {
		op  string
		cli []string
	}{
		{"setup.discover", []string{"setup", "discover"}},
		{"setup.aliases", []string{"setup", "aliases"}},
		{"setup.import_scan", []string{"setup", "import-scan"}},
		{"skill.status", []string{"skill", "status"}},
	} {
		st, rest := post(c.op, "{}")
		cli := csOK(t, m.Lore(t, repo, append(c.cli, "--json")...))
		drop := []string{"checked_at", "took_ms", "dir", "root", "here", "probed_at", "cached"}
		if st != 200 || csCanon(t, cli, drop...) != csCanon(t, rest, drop...) {
			t.Errorf("%s: REST %d differs from CLI\ncli  %s\nrest %s", c.op, st, csCanon(t, cli, drop...), rest)
		}
	}
}

func csListed(l listOut, name string) bool {
	for _, s := range l.Setups {
		if s.Name == name {
			return true
		}
	}
	return false
}
