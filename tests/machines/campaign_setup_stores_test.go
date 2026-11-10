//go:build machines && !windows

package machines

// campaign_setup_stores_test.go — the setup steps that reach a remote: an
// invite line, the personal store step against an unreachable remote and
// a real bare repo, and a second machine reading what the first wrote.

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// csInvite is a v1 invite line for org acme at store.
func csInvite(t *testing.T, store, expires string) string {
	t.Helper()
	type invite struct {
		V         int    `json:"v"`
		ID        string `json:"id"`
		Org       string `json:"org"`
		Store     string `json:"store"`
		OrgFolder string `json:"org_folder"`
		Email     string `json:"email"`
		By        string `json:"by"`
		Expires   string `json:"expires"`
	}
	b, err := json.Marshal(invite{V: 1, ID: "inv_01936f2a7c4b7e1d8a3f5b6c7d8e9f00", Org: "acme", Store: store,
		Email: "alice@example.com", By: "bob@example.com", Expires: expires})
	if err != nil {
		t.Fatal(err)
	}
	return "lore-invite:v1:" + base64.RawURLEncoding.EncodeToString(b)
}

// TestCampaignSetup_Join: a whole invite line (also pasted with the
// command in front) opens the join flow in its documented shape and
// writes nothing; a cut or empty line is E_INVITE_INVALID.
func TestCampaignSetup_Join(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	inv := csInvite(t, "git@github.com:acme/notes.git", "2030-01-01")
	before := csConfig(t, m)
	for _, line := range []string{inv, "lore setup join " + inv, "  " + inv + "==  "} {
		r := m.Lore(t, "", "setup", "join", line, "--no-open", "--json")
		out := csOK(t, r)
		csConforms(t, "setup.join", out)
		var o struct {
			Web struct {
				Setup string `json:"setup"`
			} `json:"web"`
			Invite struct {
				Org   string `json:"org"`
				Email string `json:"email"`
			} `json:"invite"`
		}
		_ = json.Unmarshal([]byte(out), &o)
		if o.Web.Setup != "acme" || o.Invite.Org != "acme" || o.Invite.Email != "alice@example.com" {
			t.Errorf("join %.30q: %+v", line, o)
		}
	}
	for _, bad := range []string{inv[:40], "lore-invite:v1:", "lore-invite:v2:abc", strings.Repeat("x", 10000)} {
		e := csRefused(t, m.Lore(t, "", "setup", "join", bad, "--no-open", "--json"), "E_INVITE_INVALID")
		if e != nil && len(e.Message) > 500 {
			t.Errorf("join refusal echoes the input: %d bytes", len(e.Message))
		}
	}
	if csConfig(t, m) != before {
		t.Errorf("setup join wrote config.yaml")
	}
}

// csToGitHub saves kind, identity and github (alpha over github.com) of a
// solo setup on a fresh machine.
func csToGitHub(t *testing.T, m *Machine, name string) {
	t.Helper()
	csNewSetup(t, m, name, PersonEmail)
	csOK(t, m.Lore(t, "", "setup", "apply", name, "--step", "github", "--answers", `{"github_account":"`+LoginAlpha+`","host_alias":"github.com"}`, "--json"))
}

// TestCampaignSetup_PersonalStore: the personal store step refuses an
// unreachable remote with E_SETUP_STEP_FAILED and its fix, writing no
// store url; a real bare repo is created as a store, and a second
// machine using the same repo reads the first machine's profile.
func TestCampaignSetup_PersonalStore(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	csToGitHub(t, m, "solo")
	for _, url := range []string{"git@unreachable.invalid:pat/lore-me.git", "file://" + filepath.Join(m.Dir, "no-such.git")} {
		before := csConfig(t, m)
		ans, _ := json.Marshal(map[string]string{"personal_store": "existing", "store_url": url})
		r := m.Lore(t, "", "setup", "apply", "solo", "--step", "personal_store", "--answers", string(ans), "--json")
		e := csRefused(t, r, "E_SETUP_STEP_FAILED")
		if e != nil && !strings.Contains(r.Stdout, "store.read") {
			t.Errorf("%s: the refusal names no failing check: %s", url, r.Stdout)
		}
		if csConfig(t, m) != before {
			t.Errorf("%s: a failed store step wrote config.yaml", url)
		}
		// check says the same without failing the command.
		chk := csOK(t, m.Lore(t, "", "setup", "check", "solo", "--step", "personal_store", "--answers", string(ans), "--json"))
		csConforms(t, "setup.check", chk)
		if !strings.Contains(chk, `"fail"`) {
			t.Errorf("%s: check reports no failure: %s", url, chk)
		}
	}
	// A bare git repository named by file:// is refused (a folder store
	// would land in git's own folder); an empty folder becomes the store.
	gitRepo := filepath.Join(m.Dir, "me.git")
	if out, err := exec.Command("/usr/bin/git", "init", "-q", "--bare", gitRepo).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v %s", err, out)
	}
	ans, _ := json.Marshal(map[string]string{"personal_store": "create", "store_url": "file://" + gitRepo})
	csRefused(t, m.Lore(t, "", "setup", "apply", "solo", "--step", "personal_store", "--answers", string(ans), "--json"), "E_INVALID_INPUT")
	bare := filepath.Join(m.Dir, "me")
	if err := os.Mkdir(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	ans, _ = json.Marshal(map[string]string{"personal_store": "create", "store_url": "file://" + bare})
	r := m.Lore(t, "", "setup", "apply", "solo", "--step", "personal_store", "--answers", string(ans), "--json")
	csConforms(t, "setup.apply", csOK(t, r))
	again := m.Lore(t, "", "setup", "apply", "solo", "--step", "personal_store", "--answers", string(ans), "--json")
	var o struct {
		Changed bool `json:"changed"`
	}
	if json.Unmarshal([]byte(csOK(t, again)), &o) != nil || o.Changed {
		t.Errorf("the store step a second time changed something: %s", again.Stdout)
	}
	if s := csOK(t, m.Lore(t, "", "setup", "show", "solo", "--json")); !strings.Contains(s, bare) {
		t.Errorf("show lacks the store url: %s", s)
	}
	// Machine one labels itself in the store; machine two, pointed at the
	// same repo, lists that profile.
	repo := m.Repos[RepoLore]
	csOK(t, m.Lore(t, "", "setup", "edit", "solo", "--default", "--json"))
	csOK(t, m.Lore(t, repo, "machine", "label", "machine-one", "--json"))
	m2 := Build(t, ShapeFresh, loreBin)
	csOK(t, m2.Lore(t, "", "identity", "set", "two@example.test", "--json"))
	csOK(t, m2.Lore(t, "", "config", "set", "personal.store", "file://"+bare))
	list := csOK(t, m2.Lore(t, m2.Repos[RepoLore], "machine", "list", "--json"))
	if !strings.Contains(list, "machine-one") {
		t.Errorf("machine two does not see machine one's profile: %s", list)
	}
}

// TestCampaignSetup_ConcurrentMixed: different setup writes from parallel
// processes (new setups, an edit, a disable, an identity) all land, or the
// loser is told to retry; config.yaml stays valid for every reader.
func TestCampaignSetup_ConcurrentMixed(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeMigrated, loreBin)
	csNewSetup(t, m, "togg", "togg@example.test")
	jobs := [][]string{
		{"setup", "apply", "mix-a", "--step", "kind", "--answers", csKindAnswers("mix-a")},
		{"setup", "apply", "mix-b", "--step", "kind", "--answers", csKindAnswers("mix-b")},
		{"setup", "edit", "main", "--note", "edited in parallel"},
		{"setup", "disable", "togg"},
		{"identity", "set", "--org", "", "alice@example.com"},
	}
	res := make([]Result, len(jobs))
	done := make(chan int, len(jobs))
	for i, j := range jobs {
		go func() { res[i] = m.Lore(t, "", append(j, "--json")...); done <- i }()
	}
	for range jobs {
		<-done
	}
	for i, r := range res {
		if r.Code != 0 {
			if e := csErrOf(r); e == nil || !e.Retryable {
				t.Errorf("%v failed without a retryable refusal: %s", jobs[i], r)
			}
		}
	}
	l := m.List(t, "")
	want := map[int]func() bool{
		0: func() bool { return csListed(l, "mix-a") },
		1: func() bool { return csListed(l, "mix-b") },
		2: func() bool { return strings.Contains(csConfig(t, m), "edited in parallel") },
		3: func() bool {
			for _, s := range l.Setups {
				if s.Name == "togg" {
					return s.Disabled
				}
			}
			return false
		},
		4: func() bool { return strings.Contains(csConfig(t, m), "human:alice@example.com") },
	}
	for i, ok := range want {
		if res[i].Code == 0 && !ok() {
			t.Errorf("%v reported success but its write is gone (lost update):\n%s", jobs[i], csConfig(t, m))
		}
	}
	csOK(t, m.Lore(t, "", "config", "check"))
}

// TestCampaignSetup_ConcurrentFirstUse: on a machine that never ran lore,
// several lore processes started together (an agent fanning out) all
// work: the first open of machine.db and the first config.yaml writes do
// not fail the others with "database is locked" or lose an entry.
func TestCampaignSetup_ConcurrentFirstUse(t *testing.T) {
	t.Parallel()
	for round := range 3 {
		m := Build(t, ShapeFresh, loreBin)
		names := []string{"first-a", "first-b", "first-c", "first-d", "first-e", "first-f"}
		res := make([]Result, len(names))
		done := make(chan struct{}, len(names))
		for i, n := range names {
			go func() {
				res[i] = m.Lore(t, "", "setup", "apply", n, "--step", "kind", "--answers", csKindAnswers(n), "--json")
				done <- struct{}{}
			}()
		}
		for range names {
			<-done
		}
		l := m.List(t, "")
		for i, r := range res {
			if r.Code != 0 {
				t.Errorf("round %d: %s", round+1, r)
			} else if !csListed(l, names[i]) {
				t.Errorf("round %d: %s said ok but is not listed", round+1, names[i])
			}
		}
		// Every save took "no setup is the default yet"; exactly one won.
		if n := strings.Count(csConfig(t, m), "default: true"); n != 1 {
			t.Errorf("round %d: %d setups are the default, want 1:\n%s", round+1, n, csConfig(t, m))
		}
	}
}
