//go:build userscen

package userscen

// campaign_config_cmds_test.go — the config area the way a person uses
// it: lore config get/set/list/check, lore repo add/list/archive/
// unarchive/delete with the trash, and lore upgrade / version with odd input. Each row of a matrix is one
// thing a user types; refusals are held to their code, exit and next step.
//
//   - config set → get → list round trip for every value kind (bool,
//     duration, text with spaces, unicode and a 10k value), set twice is
//     byte-identical, comments and key order survive
//   - config set refusals (wrong type, path through a mapping, unknown key,
//     policy key) name the cause and a next step and write nothing
//   - config get/list --json shape, and the same answer over `lore api`
//   - repo add → list → archive → unarchive → delete → trash restore, a
//     taken name, invalid names; --json and the REST list agree
//   - upgrade and version with odd flags never reach the network or crash

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// ccLongValue is a 10k-character value (a pasted blob).
var ccLongValue = strings.Repeat("x", 10_000)

// ccConfigEntry is one key of `lore config get|list --json`.
type ccConfigEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ccConfigList is `lore config list --json`.
type ccConfigList struct {
	Entries []ccConfigEntry `json:"entries"`
	Count   int             `json:"count"`
}

// TestCampaignConfig_ConfigRoundTrip: set → get → list for every kind of
// value, in and outside a repo; set twice writes the same bytes; comments
// and order are kept.
func TestCampaignConfig_ConfigRoundTrip(t *testing.T) {
	w, a, c := cliSolo(t)
	dir := cliOutside(t, w, a)
	path := a.LorePath("config.yaml")
	const header = "# my lore settings\nlive:\n  # push at once\n  autopush: true\n"
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, key, value string
	}{
		{"bool", "live.autopush", "false"},
		{"duration", "lease.ttl", "45m"},
		{"identity key", "identity.key", "human:alice@example.com"},
		{"text with spaces and unicode", "identity.name", "Alice Ünïcode 🚀 مرحبا"},
		{"10k text", "identity.name", ccLongValue},
	}
	for _, where := range []string{dir, c.Dir} {
		for _, tc := range cases {
			a.Lore(where, "config", "set", tc.key, tc.value).OK(t).Says(t, "✓ "+tc.key+" = ")
			first, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			a.Lore(where, "config", "set", tc.key, tc.value).OK(t)
			if second, _ := os.ReadFile(path); string(second) != string(first) {
				t.Errorf("%s: a second identical set changed the file:\n%s\n---\n%s", tc.name, first, second)
			}
			if got := strings.TrimSuffix(a.Lore(where, "config", "get", tc.key).OK(t).Stdout, "\n"); got != tc.value {
				t.Errorf("%s: get %s = %.80q, want %.80q", tc.name, tc.key, got, tc.value)
			}
			e := th.DecodeJSON[ccConfigEntry](t, a.Lore(where, "config", "get", tc.key, "--json"))
			if e.Key != tc.key || e.Value != tc.value {
				t.Errorf("%s: get --json = {%q %.80q}", tc.name, e.Key, e.Value)
			}
		}
	}
	raw, _ := os.ReadFile(path)
	for _, keep := range []string{"# my lore settings", "# push at once"} {
		if !strings.Contains(string(raw), keep) {
			t.Errorf("config set dropped the comment %q:\n%s", keep, raw)
		}
	}
	if strings.Index(string(raw), "live:") > strings.Index(string(raw), "lease:") {
		t.Errorf("config set reordered the keys (live was first):\n%s", raw)
	}
	l := th.DecodeJSON[ccConfigList](t, a.Lore(dir, "config", "list", "--json"))
	if l.Count != len(l.Entries) || l.Count != 4 {
		t.Fatalf("config list --json: want 4 keys, count == len(entries): %+v", l)
	}
	for i := 1; i < len(l.Entries); i++ {
		if l.Entries[i-1].Key >= l.Entries[i].Key {
			t.Errorf("config list is not sorted by key: %+v", l.Entries)
		}
	}
	a.Lore(dir, "config", "check").OK(t).Says(t, "is valid")
	c.MustLore("rule", "list") // the file it wrote is one lore reads
}

// TestCampaignConfig_ConfigRefusals: every refused set names its cause and
// a next step and leaves the file byte for byte as it was; a missing key
// on get points at config list.
func TestCampaignConfig_ConfigRefusals(t *testing.T) {
	w, a, c := cliSolo(t)
	dir := cliOutside(t, w, a)
	path := a.LorePath("config.yaml")
	if err := os.WriteFile(path, []byte("live:\n  autopush: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, key, value string
		say              []string
	}{
		{"bool gets text", "live.autopush", "maybe", []string{`bad value "maybe"`, "expected true or false", "hint: nothing written; docs/reference/config.md"}},
		{"duration in days", "lease.ttl", "1d12h", []string{`bad value "1d12h"`, `invalid duration "1d12h"`, "hint: nothing written"}},
		{"list item that does not exist", "orgs.0.name", "acme", []string{"orgs: expected a list", "hint: nothing written"}},
		{"a mapping as a scalar", "live", "3", []string{`"live" holds a list or a mapping`, "hint: nothing written; `lore config list`"}},
		{"below a scalar", "live.autopush.x", "1", []string{`"x" does not exist under a list or a value`, "hint: nothing written; `lore config list`"}},
		{"unknown key", "bogus.key", "1", []string{"is not a key lore reads", "hint: nothing written"}},
		{"policy key", "policies.trash.retention_days", "30", []string{"is a policy key", "hint: nothing written"}},
		{"empty key", "", "1", []string{"[E_INVALID_INPUT]", "hint:"}},
	}
	for _, where := range []string{dir, c.Dir} {
		for _, tc := range cases {
			raw, _ := os.ReadFile(path)
			r := a.Lore(where, "config", "set", tc.key, tc.value).Fails(t, cliExitUsage, append([]string{"ERROR: [E_INVALID_INPUT]"}, tc.say...)...)
			if r.Stdout != "" {
				t.Errorf("%s: a refusal prints nothing on stdout: %s", tc.name, r)
			}
			if now, _ := os.ReadFile(path); string(now) != string(raw) {
				t.Errorf("%s: a refused set changed config.yaml:\n%s", tc.name, now)
			}
		}
		for _, key := range []string{"nope", "live.nope", "orgs.7.name"} {
			a.Lore(where, "config", "get", key).Fails(t, th.ExitNotFound, "[E_NOT_FOUND] config key "+fmt.Sprintf("%q", key), "hint: `lore config list`")
		}
	}
}

// TestCampaignConfig_ConfigREST: config get/list/set over `lore api` give
// what the CLI gives with --json.
func TestCampaignConfig_ConfigREST(t *testing.T) {
	_, a, c := cliSolo(t)
	c.MustLore("config", "set", "lease.ttl", "45m")
	api := restStart(t, c)
	cliList := th.DecodeJSON[ccConfigList](t, c.Lore("config", "list", "--json"))
	rep := api.do(t, restReq{method: "GET", path: "/v1/configs"})
	var restList ccConfigList
	if rep.status != 200 || json.Unmarshal([]byte(rep.body), &restList) != nil || fmt.Sprint(restList) != fmt.Sprint(cliList) {
		t.Errorf("GET /v1/configs = %d %s, CLI list --json %+v", rep.status, rep.body, cliList)
	}
	rep = api.do(t, restReq{method: "POST", path: "/v1/configs/get", body: `{"key":"lease.ttl"}`})
	var e ccConfigEntry
	if rep.status != 200 || json.Unmarshal([]byte(rep.body), &e) != nil || e.Value != "45m" {
		t.Errorf("POST /v1/configs/get = %d %s", rep.status, rep.body)
	}
	rep = api.do(t, restReq{method: "POST", path: "/v1/configs/get", body: `{"key":"nope"}`})
	if rep.status != 404 || rep.err == nil || rep.err.Code != "E_NOT_FOUND" || rep.err.Hint == "" {
		t.Errorf("POST /v1/configs/get unknown key: want 404 E_NOT_FOUND with a hint: %d %s", rep.status, rep.body)
	}
	rep = api.do(t, restReq{method: "POST", path: "/v1/configs/set", body: `{"key":"live.autopush","value":"maybe"}`})
	if rep.status != 400 || rep.err == nil || rep.err.Code != "E_INVALID_INPUT" || !strings.Contains(rep.err.Hint, "nothing written") {
		t.Errorf("POST /v1/configs/set bad value: want 400 E_INVALID_INPUT, hint nothing written: %d %s", rep.status, rep.body)
	}
	if got := strings.TrimSpace(a.Lore(c.Dir, "config", "get", "lease.ttl").OK(t).Stdout); got != "45m" {
		t.Errorf("after the REST calls lease.ttl = %q", got)
	}
}

// ccRepo is one row of `lore repo list --json`.
type ccRepo struct {
	ID         string `json:"id"`
	MountName  string `json:"mount_name"`
	ArchivedAt string `json:"archived_at"`
}

// ccRepoList is `lore repo list --json`.
type ccRepoList struct {
	Kind  string   `json:"kind"`
	Count int      `json:"count"`
	Data  []ccRepo `json:"data"`
}

func ccRepos(t *testing.T, c *th.Clone) map[string]ccRepo {
	t.Helper()
	l := th.DecodeJSON[ccRepoList](t, c.Lore("repo", "list", "--json"))
	if l.Kind != "repo.list" || l.Count != len(l.Data) {
		t.Fatalf("repo list --json envelope: %+v", l)
	}
	out := map[string]ccRepo{}
	for _, r := range l.Data {
		out[r.MountName] = r
	}
	return out
}

// TestCampaignConfig_RepoLifecycle: add → list → archive → unarchive →
// delete → trash restore, list after each; a taken name and invalid names
// are refused with a next step.
func TestCampaignConfig_RepoLifecycle(t *testing.T) {
	_, _, c := cliSolo(t)
	c.MustLore("repo", "add", "web", "--display-name", "Web 🚀 front end").Says(t, "repo web registered (rep_")
	web, ok := ccRepos(t, c)["web"]
	if !ok || !strings.HasPrefix(web.ID, "rep_") {
		t.Fatalf("web is not in repo list --json: %+v", ccRepos(t, c))
	}
	taken := []string{"ERROR: [E_MOUNT_NAME_TAKEN]", `"web" already exists`, "hint: pick another mount name; `lore repo list`"}
	c.Lore("repo", "add", "web").Fails(t, th.ExitRefused, taken...)
	for _, bad := range []string{"my repo", "a/b", "Web", "wéb", strings.Repeat("ü", 300)} {
		c.Lore("repo", "add", bad).Fails(t, cliExitUsage, "ERROR: [E_INVALID_IDENTIFIER]", "hint: use lowercase letters")
	}
	c.Lore("repo", "add").Fails(t, cliExitUsage, "missing required field mount_name", "hint: give mount_name as argument 1")

	c.MustLore("repo", "archive", web.ID).Says(t, "web archived")
	if r := ccRepos(t, c)["web"]; r.ArchivedAt == "" {
		t.Errorf("archived repo has no archived_at: %+v", r)
	}
	c.MustLore("repo", "list").Says(t, "web [archived]")
	c.Lore("repo", "add", "web").Fails(t, th.ExitRefused, append(taken, "lore repo unarchive")...) // an archived repo keeps its name
	c.MustLore("repo", "archive", web.ID)                                                          // twice: still archived, no error
	c.MustLore("repo", "unarchive", web.ID).Says(t, "web unarchived")
	if r := ccRepos(t, c)["web"]; r.ArchivedAt != "" {
		t.Errorf("unarchived repo keeps archived_at: %+v", r)
	}
	c.Lore("repo", "delete", web.ID).Fails(t, th.ExitNeedsConfirm, "[E_NEEDS_CONFIRM]", "hint:")
	if _, ok := ccRepos(t, c)["web"]; !ok {
		t.Fatal("an unconfirmed delete removed the repo")
	}
	c.MustLore("repo", "delete", web.ID, "--yes").Says(t, "moved to the trash", "lore trash restore --target "+web.ID)
	if _, ok := ccRepos(t, c)["web"]; ok {
		t.Fatal("deleted repo still listed")
	}
	c.MustLore("trash", "restore", web.ID)
	if r, ok := ccRepos(t, c)["web"]; !ok || r.ID != web.ID {
		t.Fatalf("trash restore did not bring web back with its id: %+v", ccRepos(t, c))
	}
	for _, verb := range []string{"archive", "unarchive"} {
		c.Lore("repo", verb, "rep_nope").Fails(t, cliExitUsage, "[E_INVALID_ID]")
		c.Lore("repo", verb).Fails(t, cliExitUsage, "missing required field id", "hint:")
	}
}

// TestCampaignConfig_RepoREST: repo list over `lore api` matches the CLI.
func TestCampaignConfig_RepoREST(t *testing.T) {
	_, _, c := cliSolo(t)
	c.MustLore("repo", "add", "web")
	api := restStart(t, c)
	rep := api.do(t, restReq{method: "GET", path: "/v1/repos"})
	var rest ccRepoList
	if rep.status != 200 || json.Unmarshal([]byte(rep.body), &rest) != nil {
		t.Fatalf("GET /v1/repos = %d %s", rep.status, rep.body)
	}
	cli := ccRepos(t, c)
	if len(rest.Data) != len(cli) {
		t.Fatalf("REST lists %d repos, CLI %d: %s", len(rest.Data), len(cli), rep.body)
	}
	for _, r := range rest.Data {
		if cli[r.MountName].ID != r.ID {
			t.Errorf("REST repo %+v is not the CLI's %+v", r, cli[r.MountName])
		}
	}
	rep = api.do(t, restReq{method: "POST", path: "/v1/repos", body: `{"mount_name":"web"}`})
	if rep.err == nil || rep.err.Code != "E_MOUNT_NAME_TAKEN" || rep.err.Hint == "" {
		t.Errorf("POST /v1/repos with a taken name: want E_MOUNT_NAME_TAKEN with a hint: %d %s", rep.status, rep.body)
	}
}

// TestCampaignConfig_UpgradeOddInput: lore upgrade with odd versions and
// flags, in read-only mode too, never crashes and never takes the lock;
// a source build says how to update. The proxies point at a closed port,
// so a request could not reach the internet.
func TestCampaignConfig_UpgradeOddInput(t *testing.T) {
	w, a, c := cliSolo(t)
	dir := cliOutside(t, w, a)
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"} {
		a.SetEnv(k, "http://127.0.0.1:9")
	}
	a.SetEnv("NO_PROXY", "")
	for _, where := range []string{dir, c.Dir} {
		for _, args := range [][]string{
			{"upgrade", "--version", ""}, {"upgrade", "--version", "not-a-version"}, {"upgrade", "--version", "v9.9.9-ü"},
			{"upgrade", "run", "--check", "--json"}, {"upgrade", "--read-only", "--check"},
		} {
			r := a.Lore(where, args...)
			ccNoStack(t, cliProc{Args: r.Args, Stdout: r.Stdout, Stderr: r.Stderr, Code: r.Code})
			if r.Code == th.ExitError {
				t.Errorf("exit 1 (E_INTERNAL): %s", r)
			}
		}
		a.Lore(where, "version", "--json", "extra").Fails(t, cliExitUsage, `"code": "E_INVALID_INPUT"`)
	}
	if _, err := os.Stat(a.LorePath("state", "upgrade.lock")); err == nil {
		t.Errorf("a source build took the upgrade lock")
	}
}
