//go:build machines && !windows

package machines

// campaign_setup_followups_test.go — follow-ups of the setup campaign:
// every setup name reachable over REST (the trash routes moved off
// /v1/setup/setup), a trash purge that leaves nothing of the setup in
// machine.db, setup edit refusing what the setup steps refuse, a CLI kind
// step that takes the question defaults, and org config writers that share
// the config.yaml lock.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestCampaignSetup_SetupNamedSetupOverREST: a setup named `setup` (or
// `trash`) shows over REST like any other, and the trash list answers at
// its own documented path, listing what `lore setup remove` moved there.
func TestCampaignSetup_SetupNamedSetupOverREST(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeMigrated, loreBin)
	repo := m.Repos[RepoLore]
	for _, name := range []string{"setup", "trash", "gone"} {
		csNewSetup(t, m, name, name+"@example.com")
	}
	csOK(t, m.Lore(t, repo, "setup", "remove", "gone", "--json"))
	api := csStartAPI(t, m, repo)
	for _, name := range []string{"setup", "trash"} {
		st, body := api.call(t, "GET", "/v1/setup/"+name, "")
		var o struct {
			Setup struct {
				Name string `json:"name"`
			} `json:"setup"`
		}
		if st != 200 || json.Unmarshal([]byte(body), &o) != nil || o.Setup.Name != name {
			t.Errorf("GET /v1/setup/%s: %d %s, want the setup %s", name, st, body, name)
		}
	}
	rt := csRouteOf(t, "setup.trash.list")
	if rt.Method != "GET" || rt.Path != "/v1/setup/trash/setups" {
		t.Errorf("setup.trash.list is documented at %s %s", rt.Method, rt.Path)
	}
	st, body := api.call(t, rt.Method, rt.Path, "")
	if st != 200 || !strings.Contains(body, `"gone"`) || strings.Contains(body, `"trash"`) {
		t.Errorf("trash list: %d %s, want the trashed setup gone only", st, body)
	}
	csConforms(t, "setup.trash.list", body)
	for _, opID := range []string{"setup.trash.restore", "setup.trash.purge"} {
		if p := csRouteOf(t, opID).Path; !strings.HasPrefix(p, "/v1/setup/trash/setups/") {
			t.Errorf("%s is documented at %s", opID, p)
		}
	}
}

// TestCampaignSetup_PurgeLeavesNothingInMachineDB: `lore setup trash
// purge` deletes the setup's step progress for good. Before, the rows were
// only soft-deleted: machine.db kept every saved answer (the email too) in
// documents `lore trash` still listed. Covers a finished setup (its rows
// were already in the row trash) and a half-made one (live rows), and
// leaves another setup's progress alone.
func TestCampaignSetup_PurgeLeavesNothingInMachineDB(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeMigrated, loreBin)
	csNewSetup(t, m, "zebra-full", "zebra-full@example.com")
	csOK(t, m.Lore(t, "", "setup", "apply", "zebra-half", "--step", "kind", "--answers", csKindAnswers("zebra-half"), "--json"))
	csOK(t, m.Lore(t, "", "setup", "apply", "keeper", "--step", "kind", "--answers", csKindAnswers("keeper"), "--json"))
	needles := []string{"zebra-full", "zebra-half"}
	if hits := csMachineDBHits(t, m, append(needles, "keeper")...); len(hits) == 0 {
		t.Fatal("machine.db holds no setup progress before the purge: the test checks nothing")
	}
	for _, name := range needles {
		csOK(t, m.Lore(t, "", "setup", "remove", name, "--json"))
		csOK(t, m.Lore(t, "", "setup", "trash", "purge", name, "--confirm", "--json"))
	}
	for _, h := range csMachineDBHits(t, m, append(needles, "zebra-full@example.com")...) {
		t.Errorf("after the purge machine.db still names the setup: %s", h)
	}
	if hits := csMachineDBHits(t, m, "keeper"); len(hits) == 0 {
		t.Error("the purge also removed another setup's progress")
	}
	if r := m.Lore(t, m.Repos[RepoLore], "trash", "list", "--json"); strings.Contains(r.Stdout, "zebra") {
		t.Errorf("lore trash still lists the purged setup's progress: %s", r.Stdout)
	}
	// The name is free again: a new setup of that name saves its steps.
	csNewSetup(t, m, "zebra-full", "zebra-again@example.com")
	if s := csOK(t, m.Lore(t, "", "setup", "show", "zebra-full", "--json")); !strings.Contains(s, "zebra-again@example.com") {
		t.Errorf("a setup made again after the purge: %s", s)
	}
}

// csMachineDBHits queries every table of the lore home's machine.db files
// (the machine store and its cache) and returns "file table: row" for each
// row whose columns contain one of needles.
func csMachineDBHits(t *testing.T, m *Machine, needles ...string) []string {
	t.Helper()
	var hits []string
	for _, rel := range []string{filepath.Join(".lore", "state", "machine.db"), filepath.Join(".lore", "cache", "machine.db")} {
		path := filepath.Join(m.Home, rel)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		tables, err := csColumn(db, `SELECT name FROM sqlite_master WHERE type = 'table'`)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		for _, tbl := range tables {
			cols, err := csColumn(db, `SELECT name FROM pragma_table_info('`+tbl+`')`)
			if err != nil {
				t.Fatalf("%s %s: %v", rel, tbl, err)
			}
			if len(cols) == 0 {
				continue
			}
			// One text per row: every column cast to text (a BLOB
			// document reads as its bytes), joined.
			parts := make([]string, len(cols))
			for i, c := range cols {
				parts[i] = `COALESCE(CAST("` + c + `" AS TEXT), '')`
			}
			lines, err := csColumn(db, `SELECT `+strings.Join(parts, ` || '|' || `)+` FROM "`+tbl+`"`)
			if err != nil {
				t.Fatalf("%s %s: %v", rel, tbl, err)
			}
			for _, line := range lines {
				for _, n := range needles {
					if strings.Contains(line, n) {
						hits = append(hits, rel+" "+tbl+": "+n)
					}
				}
			}
		}
		_ = db.Close()
	}
	return hits
}

// csColumn is the first column of every row q returns.
func csColumn(db *sql.DB, q string) ([]string, error) {
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// TestCampaignSetup_EditRefusesWhatTheStepsRefuse: setup edit judges
// --github-user, --host-alias, --ssh-key and --folder by the rules the
// github and folders steps apply when the setup is made (question rules,
// the route validator every git and ssh call runs, a key file that
// exists). A refusal is E_INVALID_INPUT, says nothing was saved, and
// leaves config.yaml as it was; good values still save.
func TestCampaignSetup_EditRefusesWhatTheStepsRefuse(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeMigrated, loreBin)
	csNewSetup(t, m, "ed", "ed@example.com")
	key := filepath.Join(m.Home, ".ssh", "id_fsetup")
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("placeholder key file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := csConfig(t, m)
	for _, c := range []struct {
		flag, value, want string
	}{
		{"--github-user", "-octo", "GitHub login"},
		{"--github-user", "octo_cat", "GitHub login"},
		{"--github-user", strings.Repeat("a", 40), "longer than 39"},
		{"--host-alias", "-oProxyCommand=touch", "host alias"},
		{"--host-alias", "github;acme", "host alias"},
		{"--host-alias", strings.Repeat("h", 200), "longer than"},
		{"--ssh-key", "id_fsetup", "starts with / or ~/"},
		{"--ssh-key", "~/.ssh/id_missing", "no key file at"},
		{"--ssh-key", "/no/such/key", "no key file at"},
		{"--ssh-key", "~/.ssh", "no key file at"},
		{"--folder", "code/**", "must be absolute"},
		{"--folder", "~/" + strings.Repeat("d", 2000), "longer than"},
	} {
		t.Run(c.flag+" "+c.value[:min(len(c.value), 20)], func(t *testing.T) {
			e := csRefused(t, m.Lore(t, "", "setup", "edit", "ed", c.flag, c.value, "--json"), "E_INVALID_INPUT")
			if e == nil {
				return
			}
			if !strings.Contains(e.Message, c.want) || !strings.Contains(e.Hint, "nothing was saved") {
				t.Errorf("message %q hint %q, want %q and that nothing was saved", e.Message, e.Hint, c.want)
			}
			if after := csConfig(t, m); after != before {
				t.Errorf("a refused edit changed config.yaml:\n%s", after)
			}
		})
	}
	// Good values save, each judged alone (an older saved value is not
	// re-judged by an edit of another flag).
	for _, args := range [][]string{
		{"--github-user", "octo-cat"},
		{"--host-alias", "github-acme"},
		{"--ssh-key", "~/.ssh/id_fsetup"},
		{"--ssh-key", key},
		{"--host-alias", "github.com"},
		{"--folder", "~/clients/acme/**", "--folder", "/srv/acme/**"},
		{"--note", "edited"},
	} {
		csOK(t, m.Lore(t, "", append(append([]string{"setup", "edit", "ed"}, args...), "--json")...))
	}
	if s := csConfig(t, m); !strings.Contains(s, "octo-cat") || !strings.Contains(s, "/srv/acme/**") {
		t.Errorf("good edits did not save:\n%s", s)
	}
}

// TestCampaignSetup_CLIKindTakesPlanDefault: a CLI kind step that leaves
// out `default` takes the value setup plan shows (the page always sends
// it): the first solo setup of a fresh machine is the default one, a
// later one is not, and a given default is kept as given.
func TestCampaignSetup_CLIKindTakesPlanDefault(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	planDefault := func() bool {
		t.Helper()
		var p struct {
			Steps []struct {
				ID        string `json:"id"`
				Questions []struct {
					ID    string          `json:"id"`
					Value json.RawMessage `json:"value"`
				} `json:"questions"`
			} `json:"steps"`
		}
		if err := json.Unmarshal([]byte(csOK(t, m.Lore(t, "", "setup", "plan", "--kind", "solo", "--json"))), &p); err != nil {
			t.Fatal(err)
		}
		for _, s := range p.Steps {
			for _, q := range s.Questions {
				if s.ID == "kind" && q.ID == "default" {
					return string(q.Value) == "true"
				}
			}
		}
		t.Fatal("setup plan shows no default question on the kind step")
		return false
	}
	isDefault := func(name string) bool {
		t.Helper()
		var o struct {
			Setup struct {
				Default bool `json:"default"`
			} `json:"setup"`
		}
		if err := json.Unmarshal([]byte(csOK(t, m.Lore(t, "", "setup", "show", name, "--json"))), &o); err != nil {
			t.Fatal(err)
		}
		return o.Setup.Default
	}
	for _, c := range []struct {
		name, answers string
		want          bool
	}{
		{"solo-first", csKindAnswers("solo-first"), true},
		{"solo-second", csKindAnswers("solo-second"), false},
		{"solo-asked", `{"kind":"solo","name":"solo-asked","default":true}`, true},
		{"solo-declined", `{"kind":"solo","name":"solo-declined","default":false}`, false},
	} {
		shown := planDefault()
		csOK(t, m.Lore(t, "", "setup", "apply", c.name, "--step", "kind", "--answers", c.answers, "--json"))
		if got := isDefault(c.name); got != c.want {
			t.Errorf("%s: default = %v, want %v (plan showed %v)", c.name, got, c.want, shown)
		}
		if !strings.Contains(c.answers, `"default"`) && shown != c.want {
			t.Errorf("%s: plan showed default %v, the CLI saved %v", c.name, shown, c.want)
		}
	}
	if n := strings.Count(csConfig(t, m), "default: true"); n != 1 {
		t.Errorf("%d setups are the default, want 1:\n%s", n, csConfig(t, m))
	}
}

// TestCampaignSetup_ConcurrentOrgAndSetupWrites: org renames running
// beside setup edits and new setups (the setup page, a CLI and an agent
// at once) all land: the org config writers take the same config.yaml
// lock as the setup writers, so none writes its older copy over another
// process's edit.
func TestCampaignSetup_ConcurrentOrgAndSetupWrites(t *testing.T) {
	t.Parallel()
	const orgs, setups, rounds = 5, 4, 3
	for round := range rounds {
		m := Build(t, ShapeFresh, loreBin)
		var cfg strings.Builder
		cfg.WriteString("identity:\n    key: human:alice@example.com\n    name: Alice Example\norgs:\n")
		for i := range orgs {
			fmt.Fprintf(&cfg, "    - name: org-%d\n      identity: {key: \"human:alice@acme.example\"}\n      stores: [{url: \"git@github.com:acme/lore-%d.git\"}]\n", i, i)
		}
		cfg.WriteString("setups:\n")
		for i := range setups {
			fmt.Fprintf(&cfg, "    - name: s-%d\n      kind: solo\n", i)
		}
		path := filepath.Join(m.Home, ".lore", "config.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(cfg.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		var jobs [][]string
		for i := range orgs {
			jobs = append(jobs, []string{"org", "rename", fmt.Sprintf("org-%d", i), fmt.Sprintf("org-%d-renamed", i)})
		}
		for i := range setups {
			jobs = append(jobs, []string{"setup", "edit", fmt.Sprintf("s-%d", i), "--note", fmt.Sprintf("note %d", i)})
		}
		jobs = append(jobs, []string{"setup", "apply", "fresh-x", "--step", "kind", "--answers", csKindAnswers("fresh-x")})
		res := make([]Result, len(jobs))
		done := make(chan struct{}, len(jobs))
		for i, j := range jobs {
			go func() { res[i] = m.Lore(t, "", append(j, "--json")...); done <- struct{}{} }()
		}
		for range jobs {
			<-done
		}
		after := csConfig(t, m)
		for i, r := range res {
			if r.Code != 0 {
				t.Errorf("round %d: %v failed: %s", round+1, jobs[i], r)
			}
		}
		for i := range orgs {
			if !strings.Contains(after, fmt.Sprintf("name: org-%d-renamed", i)) {
				t.Errorf("round %d: the rename of org-%d is lost (lost update):\n%s", round+1, i, after)
			}
		}
		for i := range setups {
			if !strings.Contains(after, fmt.Sprintf("note: note %d", i)) {
				t.Errorf("round %d: the note of s-%d is lost (lost update):\n%s", round+1, i, after)
			}
		}
		if !strings.Contains(after, "name: fresh-x") {
			t.Errorf("round %d: the new setup fresh-x is lost:\n%s", round+1, after)
		}
		csOK(t, m.Lore(t, "", "config", "check"))
	}
}
