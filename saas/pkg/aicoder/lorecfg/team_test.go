package lorecfg

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// specConfig is the §5.2 example, made concrete (a real project id).
const specConfig = `
personal:
  store: git@github.com:khanakia/lore-me

orgs:
  - name: northwind
    identity:
      key: human:alice@northwind.example
      name: Alice Doe
      handle: alice
    stores:
      - url: git@github.com:northwind/lore
        branch: main
        org_folder: org
    own:   [github.com/northwind/*]
    repos: [github.com/acme/*, github.com/globex/*]
    defaults:
      client_repos: {task: northwind, memory: northwind, rule: repo, decision: repo}
    agent_details: internal
    live: {defaults: {tasks: true}, max_age: 3d}

  - name: northwind-stag
    identity: {key: "human:alice@northwind.example", name: "Alice Doe"}
    stores:
      - url: git@github.com:northwind/lore
        branch: staging
        org_folder: org

  - name: betalabs
    identity: {key: "human:alice@betalabs.example", name: "Alice Doe"}
    stores:
      - url: git@github.com:betalabs/lore
        org_folder: org
    own: [github.com/betalabs/*]

repo_orgs:
  prj_019a1b2c3d4e7f00a1b2c3d4e5f60718: northwind
  prj_019a1b2c3d4e7f00a1b2c3d4e5f60719: {org: betalabs, remotes: [github.com/betalabs/api]}

live:
  max_age: 7d
  fetch_ttl: 60s
`

func writeTeam(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, TeamFileBase+ExtYAML), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadTeamSpecExample(t *testing.T) {
	dir := writeTeam(t, specConfig)
	cfg, loc, err := LoadTeam(context.Background(), dir, Options{Notices: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("LoadTeam: %v", err)
	}
	if !loc.Exists {
		t.Fatal("loc.Exists = false")
	}
	if got := cfg.OrgNames(); strings.Join(got, ",") != "betalabs,northwind,northwind-stag" {
		t.Fatalf("orgs = %v", got)
	}
	sh := cfg.FindOrg("northwind")
	if sh.Identity.Key != "human:alice@northwind.example" || sh.Identity.Handle != "alice" {
		t.Errorf("identity = %+v", sh.Identity)
	}
	if sh.OrgFolderStore() == nil || sh.OrgFolderStore().OrgFolder != "org" {
		t.Errorf("org folder store = %+v", sh.OrgFolderStore())
	}
	if b := cfg.FindOrg("betalabs").Stores[0].EffectiveBranch(); b != DefaultStoreBranch {
		t.Errorf("default branch = %q", b)
	}
	if ro := cfg.RepoOrgs["prj_019a1b2c3d4e7f00a1b2c3d4e5f60718"]; ro.Org != "northwind" || ro.Remotes != nil {
		t.Errorf("short repo_orgs = %+v", ro)
	}
	if ro := cfg.RepoOrgs["prj_019a1b2c3d4e7f00a1b2c3d4e5f60719"]; ro.Org != "betalabs" || len(ro.Remotes) != 1 {
		t.Errorf("long repo_orgs = %+v", ro)
	}

	// live: built-in < top level < entry
	top := cfg.LiveFor(nil)
	if top.MaxAge != 7*Day || top.FetchTTL != time.Minute || !top.Autopush || top.Remote != DefaultLiveRemote {
		t.Errorf("top live = %+v", top)
	}
	ent := cfg.LiveFor(sh)
	if ent.MaxAge != 3*Day {
		t.Errorf("entry max_age = %v, want 3d", ent.MaxAge)
	}
	if !ent.Defaults["tasks"] || ent.Defaults["memories"] || !ent.Defaults["missions"] {
		t.Errorf("entry kind defaults = %v (want built-ins kept, tasks on)", ent.Defaults)
	}
	if got := cfg.LeaseFor(sh); got.TTL != DefaultLeaseTTL || got.PushRetries != DefaultLeasePushRetries {
		t.Errorf("lease = %+v", got)
	}
	if sh.EffectiveAgentDetails() != AgentDetailsInternal {
		t.Errorf("agent_details = %q", sh.EffectiveAgentDetails())
	}
}

func TestLoadTeamAbsentIsEmpty(t *testing.T) {
	cfg, loc, err := LoadTeam(context.Background(), t.TempDir(), Options{})
	if err != nil || loc.Exists || len(cfg.Orgs) != 0 {
		t.Fatalf("absent config: cfg=%+v loc=%+v err=%v", cfg, loc, err)
	}
	if cfg.LiveFor(nil).MaxAge != DefaultLiveMaxAge {
		t.Error("defaults not applied to an absent config")
	}
}

// TestValidateRefuses is the input-space table for validation: each row is
// one broken field and the words the message must contain, so a user can
// find and fix it.
func TestValidateRefuses(t *testing.T) {
	const okOrg = `
  - name: acme
    identity: {key: "human:a@acme.example"}
    stores: [{url: git@github.com:acme/lore, org_folder: org}]
`
	cases := []struct {
		name, body string
		want       []string
	}{
		{"store branch with slash (D1)", `orgs:
  - name: acme
    stores: [{url: git@github.com:acme/lore, branch: release/1}]`, []string{`stores[0].branch "release/1"`, `may not contain "/"`}},
		{"reserved folder (D1)", `orgs:
  - name: acme
    stores: [{url: git@github.com:acme/lore, org_folder: _root}]`, []string{"org_folder", "reserved"}},
		{"org name with underscore prefix", `orgs:
  - name: _acme`, []string{"name", "reserved"}},
		{"org named me", `orgs:
  - name: me`, []string{`"me" is reserved`}},
		{"org named everywhere", `orgs:
  - name: everywhere`, []string{"reserved", "everywhere"}},
		{"org named repo", `orgs:
  - name: repo`, []string{"reserved"}},
		{"uppercase org name", `orgs:
  - name: Acme`, []string{"lowercase"}},
		{"missing name", `orgs:
  - identity: {key: "human:a@b.c"}`, []string{"name: required"}},
		{"duplicate org", "orgs:" + okOrg + okOrg, []string{"already used by orgs[0]"}},
		{"two org_folders", `orgs:
  - name: acme
    stores:
      - {url: git@github.com:acme/lore, org_folder: org}
      - {url: git@github.com:acme/lore-x, org_folder: org2}`, []string{"org_folder is set on 2 stores"}},
		{"duplicate store", `orgs:
  - name: acme
    stores:
      - {url: git@github.com:acme/lore}
      - {url: https://github.com/acme/lore.git, branch: main}`, []string{"same store and branch"}},
		{"bad store url", `orgs:
  - name: acme
    stores: [{url: /srv/lore}]`, []string{"stores[0].url", "local path"}},
		{"identity not an email (TEAM-029)", `orgs:
  - name: acme
    identity: {key: "human:alice"}`, []string{"identity.key", "not an email"}},
		{"identity agent key", `orgs:
  - name: acme
    identity: {key: "agent:bot"}`, []string{"identity.key", "human:"}},
		{"handle with at", `orgs:
  - name: acme
    identity: {key: "human:a@b.c", handle: "@alice"}`, []string{"identity.handle"}},
		{"wildcard host pattern", `orgs:
  - name: acme
    repos: ["*/acme/*"]`, []string{"repos[0]", "wildcard for the host"}},
		{"bad own pattern", `orgs:
  - name: acme
    own: ["github.com/ac*me"]`, []string{"own[0]"}},
		{"client default to another org", `orgs:
  - name: acme
    defaults: {client_repos: {memory: betalabs}}`, []string{"defaults.client_repos.memory", "another org"}},
		{"client default reserved folder", `orgs:
  - name: acme
    defaults: {client_repos: {memory: acme/_live}}`, []string{"reserved"}},
		{"agent_details", `orgs:
  - name: acme
    agent_details: everyone`, []string{"agent_details"}},
		{"zero max_age", `live: {max_age: 0s}`, []string{"live: max_age"}},
		{"top-level identity not human", `identity: {key: "agent:bot"}`, []string{"identity.key", "human:"}},
		{"top-level identity handle with at", `identity: {key: "human:a@b.c", handle: "@alice"}`, []string{"identity.handle"}},
		{"bad duration", `live: {max_age: soon}`, []string{"soon"}},
		{"mixed day duration", `live: {max_age: 1d12h}`, []string{"1d12h"}},
		{"renew >= ttl", `lease: {ttl: 5m, renew_every: 5m}`, []string{"renew_every", "shorter than ttl"}},
		{"merge adapter", `merge_check: {adapter: gitlab}`, []string{"merge_check: adapter"}},
		{"repo_orgs unknown org", `repo_orgs: {prj_019a1b2c3d4e7f00a1b2c3d4e5f60718: nobody}`, []string{"repo_orgs", `"nobody" is not an entry`}},
		{"repo_orgs bad key", "orgs:" + okOrg + `repo_orgs: {myproject: acme}`, []string{"repo_orgs[myproject]", "not a project id"}},
		{"personal store", `personal: {store: ~/notes}`, []string{"personal.store"}},
		{"default_branch", `default_branch: "bad branch"`, []string{"default_branch"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeTeam(t, c.body)
			_, _, err := LoadTeam(context.Background(), dir, Options{Notices: &bytes.Buffer{}})
			if err == nil {
				t.Fatalf("accepted:\n%s", c.body)
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error lacks %q:\n%v", w, err)
				}
			}
		})
	}
}

// TestValidateReportsEveryProblem: one run lists all problems, not the first.
func TestValidateReportsEveryProblem(t *testing.T) {
	dir := writeTeam(t, `orgs:
  - name: acme
    stores: [{url: git@github.com:acme/lore, branch: a/b}]
    agent_details: nope
live: {compact_after: 0}`)
	_, _, err := LoadTeam(context.Background(), dir, Options{Notices: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("accepted")
	}
	for _, w := range []string{"branch", "agent_details", "compact_after"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("missing %q in %v", w, err)
		}
	}
}

func TestValidateAccepts(t *testing.T) {
	ok := []string{
		``,
		`orgs: []`,
		`orgs: [{name: acme}]`, // identity is required only when the entry resolves (F18)
		`orgs: [{name: acme, stores: [{url: "git@github.com:acme/lore", org_folder: "."}]}]`,
		`orgs: [{name: a.b-c, defaults: {client_repos: {task: a.b-c/clients, rule: repo}}}]`,
		`personal: {store: local}`,
		`live: {max_age: 36h, autopush: false, defaults: {hotfixes: true}}`,
		`default_branch: trunk`,
		`untrusted_hosts: ["*.client.example"]`,
	}
	for _, body := range ok {
		dir := writeTeam(t, body)
		if _, _, err := LoadTeam(context.Background(), dir, Options{Notices: &bytes.Buffer{}}); err != nil {
			t.Errorf("refused %q: %v", body, err)
		}
	}
}

func TestDurationRoundTrip(t *testing.T) {
	cases := map[string]time.Duration{"7d": 7 * Day, "0d": 0, "60s": time.Minute, "90": 90 * time.Second, "1h30m": 90 * time.Minute}
	for in, want := range cases {
		d, err := ParseDuration(in)
		if err != nil || d.D() != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, d.D(), err, want)
		}
		again, err := ParseDuration(d.String())
		if err != nil || again != d {
			t.Errorf("round trip %q → %q → %v (%v)", in, d.String(), again.D(), err)
		}
	}
	for _, bad := range []string{"", "-1d", "-5s", "1.5d", "d", "week"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("ParseDuration(%q) accepted", bad)
		}
	}
}
