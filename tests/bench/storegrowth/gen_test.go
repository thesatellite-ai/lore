package storegrowth

// gen_test.go — the generator's shape (spec item 1 test): a 1k store has
// the planned commits, merges, bulk commits, folders, refs, org file
// changes and the two held-back commits, the same bytes every run.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testSeed makes a tiny seed store with plain git (no lore) and returns
// its bare repo and the plan that continues it.
func testSeed(t *testing.T, commits int) (string, Plan) {
	t.Helper()
	root := t.TempDir()
	work, bare := filepath.Join(root, "work"), filepath.Join(root, "store.git")
	stint := `{
  "_table": "actor_periods",
  "actor_id": "act_b",
  "id": "apd_b",
  "org_role": "member"
}
`
	files := map[string]string{
		"lore-store.yaml":                   "lore_store: 4\norg_folder: org\n",
		"org/lore-org.yaml":                 "lore_org: 1\nname: acme-co\n",
		"org/actor_periods/apd_b.json":      stint,
		"_store/memories/mem_seed.json":     "{}\n",
		"team_notes/memories/mem_seed.json": "{}\n",
	}
	for p, c := range files {
		mustWrite(t, filepath.Join(work, p), c)
	}
	git(t, root, "init", "-q", "-b", "main", work)
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@acme.test", "commit", "-q", "-m", "lore: store init")
	git(t, root, "clone", "-q", "--bare", work, bare)
	tpl := func(table, prefix string, extra string) []byte {
		return []byte(`{"_table":"` + table + `","_v":2,"_hlc":"x","_o":{"store":"sto_x"},"id":"` + prefix + `_seed","body":"seed",` + extra +
			`"created_at":"x","created_by":"act_a","created_by_actor_id":"act_a","updated_at":"x","updated_by":"act_a"}`)
	}
	p := Plan{
		Commits: commits, Seed: 7, Start: time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC), Step: 3 * time.Minute,
		People: []Person{
			{Name: "Mira", Email: "mira@acme.test", ActorID: "act_a", Admin: true},
			{Name: "Tomas", Email: "tomas@acme.test", ActorID: "act_b"},
			{Name: "Erin", Email: "erin@acme.test", ActorID: "act_c"},
		},
		AgentActorID: "act_agent",
		Templates: map[string][]byte{
			"memories":  tpl("memories", "mem", ""),
			"decisions": tpl("decisions", "dec", `"title":"t",`),
			"tasks":     tpl("tasks", "tsk", `"title":"t","status":"open",`),
			"comments":  tpl("comments", "cmt", ""),
		},
		Folders: []string{"_store", "team_notes"}, RowsPerFolder: 400,
		OrgFilePath: "org/lore-org.yaml", OrgFile: []byte(files["org/lore-org.yaml"]), OrgEvery: 250,
		PromotionPath: "org/actor_periods/apd_b.json", Promotion: []byte(stint),
		Branch: "main",
	}
	return bare, p
}

func TestGenerateShape(t *testing.T) {
	bare, p := testSeed(t, 1000)
	st, err := Import(context.Background(), bare, p)
	if err != nil {
		t.Fatal(err)
	}
	count := func(args ...string) int {
		n, err := strconv.Atoi(git(t, bare, args...))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	// every planned commit is on main, after the seed's one
	if got := count("rev-list", "--count", "main"); got != st.Commits+1+st.SideCommits-len(st.Refs) {
		t.Errorf("main has %d commits, want %d (+1 seed, side commits of landings)", got, st.Commits+1+st.SideCommits-len(st.Refs))
	}
	if got := count("rev-list", "--count", "--first-parent", "main"); got != st.Commits+1 {
		t.Errorf("main's first-parent line has %d commits, want %d", got, st.Commits+1)
	}
	if st.Commits < 1000 || st.Commits > 1002 {
		t.Errorf("stats count %d commits on main, want 1000 (+1 for the held pair)", st.Commits)
	}
	if got := count("rev-list", "--count", "--merges", "main"); got != st.Merges || got < 3 || got > 25 {
		t.Errorf("merges: %d on main, stats %d; want about 1%% of 1000", got, st.Merges)
	}
	if st.Bulk < 1 || st.Bulk > 15 {
		t.Errorf("bulk commits: %d, want about 0.5%% of 1000", st.Bulk)
	}
	// org file: the admin every 250 commits, the non-admin once
	// (commit 500 is the held pair, so the admin changes at 250 and 750)
	if got := count("rev-list", "--count", "main", "--", p.OrgFilePath); got != st.OrgChanges+1 || st.OrgChanges != 3 {
		t.Errorf("org file changes: %d on main (seed included), stats %d, want 2 admin + 1 non-admin", got, st.OrgChanges)
	}
	for _, msg := range []string{"org file fix", "roster fix"} {
		who := git(t, bare, "log", "-1", "--format=%ae", "--grep", "^"+msg+"$", "main")
		if who != "tomas@acme.test" {
			t.Errorf("held commit %q: author %q, want the non-admin", msg, who)
		}
	}
	if !strings.Contains(git(t, bare, "show", "main:"+p.PromotionPath), `"org_role": "admin"`) {
		t.Error("the self-promotion is not in the tree")
	}
	// refs: signals, 15 layer refs, 30 layer-trash refs; the scratch ref is gone
	refs := git(t, bare, "for-each-ref", "--format=%(refname)")
	for prefix, want := range map[string]int{"refs/lore/signals/": 1, "refs/lore/layer/": 15, "refs/lore/layer-trash/": 30, SideRef: 0} {
		if got := strings.Count(refs, prefix); got != want {
			t.Errorf("refs under %s: %d, want %d", prefix, got, want)
		}
	}
	// rows: every table, both folders, live counts match the stats
	tree := git(t, bare, "ls-tree", "-r", "--name-only", "main")
	seedRows := strings.Count(tree, "_seed.json")
	total := 0
	for table, n := range st.Rows {
		total += n
		if got := strings.Count(tree, "/"+table+"/"); got != n && !(table == "memories" && got == n+seedRows) {
			t.Errorf("table %s: %d files at main, stats %d live rows", table, got, n)
		}
	}
	if len(st.Rows) != len(p.Templates) || total < 700 || total > 2500 {
		t.Errorf("rows: %v, want every template's table and about 1.3 rows a commit", st.Rows)
	}
	for _, f := range p.Folders {
		if !strings.Contains(tree, f+"/comments/") {
			t.Errorf("folder %s holds no generated rows", f)
		}
	}
	if st.Edits < 150 || st.Deleted < 3 {
		t.Errorf("edits %d, deletes %d: want about 27%% and 3%% of the single-row commits", st.Edits, st.Deleted)
	}
	// a must rule is only ever written by the admin (anything else is a
	// held change the plan did not ask for)
	p.Templates["rules"] = []byte(`{"_table":"rules","id":"rul_seed","body":"x","severity":"must","updated_by":"act_a"}`)
	bare3, _ := testSeed(t, 400)
	if _, err := Import(context.Background(), bare3, p); err != nil {
		t.Fatal(err)
	}
	if who := git(t, bare3, "log", "--format=%ae", "main", "--", "*/rules/*"); strings.Trim(strings.ReplaceAll(who, "mira@acme.test", ""), "\n") != "" {
		t.Errorf("rules written by a non-admin:\n%s", who)
	}

	// rows are written by the commit's author, or the admin's agent session
	row := git(t, bare, "show", "main:"+strings.Fields(git(t, bare, "ls-tree", "-r", "--name-only", "main", "--", "_store/comments"))[0])
	if strings.Contains(row, `"_o"`) || !strings.Contains(row, `"updated_by": "act_`) {
		t.Errorf("generated row keeps the template's origin or names no writer:\n%s", row)
	}

	// the same plan gives the same history
	bare2, p2 := testSeed(t, 1000)
	if _, err := Import(context.Background(), bare2, p2); err != nil {
		t.Fatal(err)
	}
	if a, b := git(t, bare, "rev-parse", "main^{tree}"), git(t, bare2, "rev-parse", "main^{tree}"); a != b {
		t.Errorf("two runs of one plan differ: tree %s vs %s", a, b)
	}
}

func TestPlanValidate(t *testing.T) {
	_, p := testSeed(t, 10)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := p
	bad.People = []Person{p.People[1], p.People[0]}
	if err := bad.Validate(); err == nil {
		t.Error("a plan whose first person is no admin validates")
	}
	bad = p
	bad.Templates = map[string][]byte{"widgets": []byte("{}")}
	if err := bad.Validate(); err == nil {
		t.Error("a table with no weight validates")
	}
	var buf bytes.Buffer
	if _, err := Generate(&buf, Plan{}); err == nil || buf.Len() != 0 {
		t.Errorf("an empty plan wrote %d bytes, err %v", buf.Len(), err)
	}
}

// git runs git in dir and returns its trimmed output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// mustWrite writes a file, making its folder.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
