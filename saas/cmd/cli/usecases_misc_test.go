package main

// Use-case tests for CLI commands no other test reached: repos, commit
// links, `lore tables`, global search, the agent directive, project-name
// inference, `lore setup`, learn-candidate filtering, tier-3 repair, the
// support bundle, tag usage counts, and small sync helpers. Each command is driven through the real binary (e2e helpers in
// sync_e2e_test.go) where one exists; pure helpers get table tests.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"dbent"
	"dbent/pkg/dbtemplate"
	"saas/pkg/aicoder/lsync"

	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opssupportbundle "github.com/thesatellite-ai/lore/ops/supportbundle"
)

// decodeData unmarshals the "data" field of a lore --json envelope.
func decodeData(t *testing.T, stdout string, v any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("not a JSON envelope: %v\n%s", err, stdout)
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		t.Fatalf("data: %v\n%s", err, env.Data)
	}
}

// initProject returns a clone with a lore project.
func initProject(t *testing.T) (*world, *clone) {
	t.Helper()
	w := newWorld(t)
	c := w.newClone("alice")
	c.lore("init", "--non-interactive", "--name=app")
	return w, c
}

// ── project / repo ──────────────────────────────────────────────────────

func TestUC_MiscRepoAddDuplicateAndInvalid(t *testing.T) {
	_, c := initProject(t)
	if r := c.lore("repo", "add", "api", "--origin", "git@example.com:acme/api.git"); !strings.Contains(r.stdout, "repo api registered") {
		t.Fatalf("repo add: %s", r.stdout)
	}
	r := c.loreAny("repo", "add", "api")
	if r.code == 0 || !strings.Contains(r.stderr, "E_MOUNT_NAME_TAKEN") || !strings.Contains(r.stderr, `"api" already exists`) {
		t.Fatalf("duplicate mount name must be E_MOUNT_NAME_TAKEN: %d %s", r.code, r.stderr)
	}
	if r := c.loreAny("repo", "add", "bad name!"); r.code == 0 || !strings.Contains(r.stderr, "E_INVALID_IDENTIFIER") {
		t.Fatalf("invalid mount name: %d %s", r.code, r.stderr)
	}
	var repos []struct {
		MountName string `json:"mount_name"`
		OriginURL string `json:"origin_url"`
	}
	decodeData(t, c.lore("repo", "list", "--json").stdout, &repos)
	// PROV1: lore init gives the checkout its permanent repo id and its own
	// repos row (mount = the folder name), so the list holds that row plus
	// exactly one "api" row with its origin (the duplicate and the invalid
	// name above wrote nothing).
	apis := 0
	for _, r := range repos {
		if r.MountName == "api" {
			apis++
			if r.OriginURL != "git@example.com:acme/api.git" {
				t.Fatalf("api origin: %+v", repos)
			}
		}
	}
	if len(repos) != 2 || apis != 1 {
		t.Fatalf("repos: %+v", repos)
	}
}

func TestUC_MiscUniqueViolationHelpers(t *testing.T) {
	t.Parallel()
	for msg, want := range map[string]bool{
		"UNIQUE constraint failed: repos.project_id, repos.mount_name": true,
		"constraint failed: UNIQUE (2067)":                             true,
		"FOREIGN KEY constraint failed":                                true,
		"no such table: repos":                                         false,
		"database is locked":                                           false,
	} {
		if got := isUniqueViolation(errors.New(msg)); got != want {
			t.Fatalf("isUniqueViolation(%q) = %v", msg, got)
		}
	}
	for _, tc := range []struct {
		s, sub string
		idx    int
	}{
		{"hello world", "world", 6},
		{"hello world", "hello", 0},
		{"hello", "hello", 0},
		{"hello", "xyz", -1},
		{"he", "hello", -1},
		{"aaab", "ab", 2},
	} {
		if got := indexOf(tc.s, tc.sub); got != tc.idx {
			t.Fatalf("indexOf(%q,%q) = %d, want %d", tc.s, tc.sub, got, tc.idx)
		}
		if got := contains(tc.s, tc.sub); got != (tc.idx >= 0) {
			t.Fatalf("contains(%q,%q) = %v", tc.s, tc.sub, got)
		}
	}
}

// ── commit links ────────────────────────────────────────────────────────

type linkRow struct {
	Sha         string `json:"sha"`
	EntityTable string `json:"entity_table"`
	EntityID    string `json:"entity_id"`
	Message     string `json:"message"`
	Author      string `json:"author"`
	CommittedAt string `json:"committed_at"`
}

func TestUC_MiscLinkAddResolvesCommits(t *testing.T) {
	_, c := initProject(t)
	mem := c.addMemory("linked knowledge")
	rule := c.addRule("linked rule")
	c.commitAll("feat: wire the linker")
	head := c.git("rev-parse", "HEAD")

	// HEAD: full sha plus message, author and time from git.
	r := c.lore("link", "add", "--entity="+mem, "--commit=HEAD")
	if !strings.Contains(r.stdout, "linked "+head[:8]+" → memories/"+mem) || !strings.Contains(r.stdout, "message: feat: wire the linker") {
		t.Fatalf("link add HEAD: %s", r.stdout)
	}
	// A short sha resolves to the full one; no auto-filled message.
	c.lore("link", "add", "--entity="+rule, "--commit="+head[:7])
	// A sha git does not know is accepted as-is (linking before a fetch).
	foreign := "deadbeefcafe1234"
	c.lore("link", "add", "--entity="+rule, "--commit="+foreign, "--message=from another clone")

	var links []linkRow
	decodeData(t, c.lore("link", "list", "--json").stdout, &links)
	if len(links) != 3 {
		t.Fatalf("links: %+v", links)
	}
	byKey := map[string]linkRow{}
	for _, l := range links {
		byKey[l.EntityID+"@"+l.Sha] = l
	}
	h := byKey[mem+"@"+head]
	if h.EntityTable != "memories" || h.Message != "feat: wire the linker" || !strings.Contains(h.Author, "alice") || h.CommittedAt == "" {
		t.Fatalf("HEAD link not auto-filled: %+v", h)
	}
	if s := byKey[rule+"@"+head]; s.EntityTable != "rules" || s.Message != "" {
		t.Fatalf("short sha must resolve to the full sha without a message: %+v", s)
	}
	if f := byKey[rule+"@"+foreign]; f.Message != "from another clone" {
		t.Fatalf("unknown sha link: %+v", f)
	}
	var filtered []linkRow
	decodeData(t, c.lore("link", "list", "--commit="+foreign, "--json").stdout, &filtered)
	if len(filtered) != 1 || filtered[0].EntityID != rule {
		t.Fatalf("filter by commit: %+v", filtered)
	}

	// Errors: the same link twice, a ref that is neither git nor a sha,
	// missing flags, an unknown entity.
	if r := c.loreAny("link", "add", "--entity="+mem, "--commit=HEAD"); r.code == 0 || !strings.Contains(r.stderr, "already linked") {
		t.Fatalf("duplicate link: %d %s", r.code, r.stderr)
	}
	if r := c.loreAny("link", "add", "--entity="+mem, "--commit=not-a-ref"); r.code == 0 || !strings.Contains(r.stderr, "could not resolve") {
		t.Fatalf("bad ref: %d %s", r.code, r.stderr)
	}
	if r := c.loreAny("link", "add", "--entity="+mem); r.code == 0 || !strings.Contains(r.stderr, "--entity and --commit are required") {
		t.Fatalf("missing --commit: %d %s", r.code, r.stderr)
	}
	if r := c.loreAny("link", "add", "--entity=mem_00000000000000000000000000000000", "--commit=HEAD"); r.code == 0 {
		t.Fatal("unknown entity must fail")
	}
}

// ── lore tables ─────────────────────────────────────────────────────────

func TestUC_MiscTableCountsAndView(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "lore.db")
	if err := dbtemplate.Copy(templateDB, path); err != nil {
		t.Fatal(err)
	}
	db := dbent.InitDB(path)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE "odd""name" (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO "odd""name" VALUES (1), (2), (3)`); err != nil {
		t.Fatal(err)
	}
	counts, err := tableCounts(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tc := range counts {
		if strings.Contains(tc.Table, "_fts") {
			t.Fatalf("fts table listed: %s", tc.Table)
		}
		if tc.Table == `odd"name` {
			found = tc.Rows == 3
		}
	}
	if !found {
		t.Fatal("a table name with a quote must be counted correctly")
	}

	in := []tableCount{{"rules", 5}, {"memories", 9}, {"tasks", 1}, {"memory_extra", 0}}
	names := func(v []tableCount) string {
		var s []string
		for _, x := range v {
			s = append(s, x.Table)
		}
		return strings.Join(s, ",")
	}
	for _, tc := range []struct{ filter, sort, want string }{
		{"", "", "memories,memory_extra,rules,tasks"},
		{"", "name:desc", "tasks,rules,memory_extra,memories"},
		{"", "count", "memories,rules,tasks,memory_extra"},
		{"", "count:asc", "memory_extra,tasks,rules,memories"},
		{"MEM", "", "memories,memory_extra"},
		{"zzz", "", ""},
	} {
		if got := names(applyTableView(append([]tableCount(nil), in...), tc.filter, tc.sort)); got != tc.want {
			t.Fatalf("filter=%q sort=%q: %s, want %s", tc.filter, tc.sort, got, tc.want)
		}
	}
}

// ── global search ───────────────────────────────────────────────────────

func TestUC_MiscGlobalSearchHydratesHits(t *testing.T) {
	_, c := initProject(t)
	mem := c.addMemory("zebra stripes confuse predators")
	longRule := "zebra crossings need a dedicated rule whose body is deliberately longer than eighty characters in total"
	rule := c.addRule(longRule)
	c.lore("decision", "add", "--title=zebra decision", "--body=we picked zebra")
	var res struct {
		Count int             `json:"count"`
		Hits  []opssearch.Hit `json:"hits"`
	}
	decodeData(t, c.lore("search", "zebra", "--json").stdout, &res)
	if res.Count < 3 {
		t.Fatalf("expected hits from three entity types: %+v", res)
	}
	seen := map[string]opssearch.Hit{}
	for _, h := range res.Hits {
		seen[h.Entity] = h
	}
	if h := seen["memory"]; h.ID != mem || h.Pretty != mem {
		t.Fatalf("memory hit not hydrated: %+v", h)
	}
	if h := seen["rule"]; h.ID != rule || len(h.Body) != 80 || !strings.HasSuffix(h.Body, "...") || !strings.HasPrefix(longRule, strings.TrimSuffix(h.Body, "...")) {
		t.Fatalf("rule body must be truncated to 80: %+v", h)
	}
	if h := seen["decision"]; h.Body != "zebra decision" || h.Pretty == "" {
		t.Fatalf("decision hit shows its title: %+v", h)
	}
	if r := c.lore("search", "qqqnomatchqqq"); !strings.Contains(r.stdout, "(no matches)") {
		t.Fatalf("no-match output: %s", r.stdout)
	}
}

func TestUC_MiscTruncBody(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"eleven chars", 10, "eleven ..."},
		{"", 5, ""},
	} {
		if got := opssearch.TruncBody(tc.in, tc.n); got != tc.want {
			t.Fatalf("truncBody(%q,%d) = %q", tc.in, tc.n, got)
		}
	}
}

// ── agent directive ─────────────────────────────────────────────────────

func TestUC_MiscDirectiveInstallRemove(t *testing.T) {
	w := newWorld(t)
	dir := t.TempDir()
	run := func(args ...string) result {
		t.Helper()
		r := w.run(dir, nil, e2eBin, args...)
		if r.code != 0 {
			t.Fatalf("lore %v: %d %s", args, r.code, r.stderr)
		}
		return r
	}
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, name)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	block := run("directive", "show").stdout
	if !strings.Contains(block, "lore") || len(block) < 100 {
		t.Fatalf("directive show: %q", block)
	}

	// Default target (TOOL NEUTRAL): a folder with no AI tool file gets
	// AGENTS.md, the cross-tool file, never CLAUDE.md. v0.1.17 defaulted
	// to CLAUDE.md.
	run("directive", "install")
	if read("AGENTS.md") != block {
		t.Fatal("new AGENTS.md must hold exactly the block")
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("TOOL NEUTRAL: the default install must not create CLAUDE.md")
	}

	// AGENTS.md with hand content: block prepended, content kept; a stale
	// block is replaced in place, not duplicated.
	hand := "# Team notes\n\nKeep this.\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	run("directive", "install", "--target=AGENTS.md,docs/RULES.md")
	agents := read("AGENTS.md")
	if agents != block+"\n"+hand {
		t.Fatalf("AGENTS.md: %q", agents)
	}
	if read(filepath.Join("docs", "RULES.md")) != block {
		t.Fatal("a target in a missing folder must be created")
	}

	// v0.1.17's `directive remove` is not in the 0.1.18 design and was
	// dropped (PASS3 DESIGN-ONLY); the remove
	// half of this case went with it.
	if r := w.run(dir, nil, e2eBin, "directive", "remove"); r.code == 0 {
		t.Fatalf("directive remove must be gone: %s", r.stdout)
	}
}

// Re-installing must leave the block byte-for-byte as `directive show`
// prints it, and a stale block must be replaced in place. The block text
// contains "$RID" (shell examples); a regexp replacement that expands
// "$name" references deletes it, so the re-installed directive tells
// agents to run `lore run step  --kind=…` with no run id.
func TestUC_MiscDirectiveReinstallIsIdempotent(t *testing.T) {
	w := newWorld(t)
	dir := t.TempDir()
	run := func(args ...string) result {
		t.Helper()
		r := w.run(dir, nil, e2eBin, args...)
		if r.code != 0 {
			t.Fatalf("lore %v: %d %s", args, r.code, r.stderr)
		}
		return r
	}
	block := run("directive", "show").stdout
	if !strings.Contains(block, "$RID") {
		t.Skip("the directive no longer contains $RID")
	}
	// TOOL NEUTRAL: the default target of an empty folder is AGENTS.md
	// (v0.1.17: CLAUDE.md); the second install finds it as the repo's tool.
	path := filepath.Join(dir, "AGENTS.md")
	run("directive", "install")
	if r := run("directive", "install"); !strings.Contains(r.stdout, "(no change)") {
		t.Errorf("re-install changed the file: %s", r.stdout)
	}
	got, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != block {
		t.Errorf("re-installed block differs from `directive show` (lost $RID: %v)", !strings.Contains(string(got), "$RID"))
	}
	hand := "\nhand notes\n"
	// An older block: same markers, different body (a line was added).
	first, rest, _ := strings.Cut(block, "\n")
	stale := first + "\nan outdated line\n" + rest + hand
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	run("directive", "install")
	if got, _ := os.ReadFile(filepath.Clean(path)); string(got) != block+hand {
		t.Errorf("stale block must be replaced in place, keeping hand notes")
	}
}

// ── project name from git ───────────────────────────────────────────────

func TestUC_MiscInferProjectNameFromGit(t *testing.T) {
	t.Parallel()
	for url, want := range map[string]string{
		"git@github.com:khanakia/aicoder-cli-go.git": "aicoder-cli-go",
		"https://github.com/vercel/next.js.git":      "next.js",
		"https://gitlab.com/org/sub/internal-api":    "internal-api",
		"ssh://git@host:2222/team/widget.git":        "widget",
		"gitlab.com:org/sub/internal-api.git":        "internal-api",
	} {
		dir := gitInitRepo(t)
		cmd := testCmd(t, "git", "remote", "add", "origin", url)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		if got := inferProjectNameFromGit(dir); got != want {
			t.Fatalf("%s → %q, want %q", url, got, want)
		}
	}
	if got := inferProjectNameFromGit(gitInitRepo(t)); got != "" {
		t.Fatalf("no origin must give \"\": %q", got)
	}
	if got := inferProjectNameFromGit(t.TempDir()); got != "" {
		t.Fatalf("not a repo must give \"\": %q", got)
	}
}

func TestUC_MiscInitNamesProjectFromOrigin(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("alice")
	c.git("remote", "set-url", "origin", "git@github.com:acme/widget-api.git")
	c.lore("init", "--non-interactive")
	var projects []struct {
		Name string `json:"name"`
	}
	decodeData(t, c.lore("project", "list", "--json").stdout, &projects)
	if len(projects) != 1 || projects[0].Name != "widget-api" {
		t.Fatalf("project named from origin: %+v", projects)
	}
}

// ── lore setup ──────────────────────────────────────────────────────────

func TestUC_MiscSetupStampsFingerprint(t *testing.T) {
	_, c := initProject(t)
	r := c.lore("setup")
	if !strings.Contains(r.stdout, "setup complete (registry fingerprint: ") {
		t.Fatalf("setup output: %s", r.stdout)
	}
	fp := computeRegistryFingerprint()
	if !strings.Contains(r.stdout, fp) {
		t.Fatalf("stamped fingerprint %s not reported: %s", fp, r.stdout)
	}
	db := dbent.InitDB(filepath.Join(c.dir, ".lore", "lore.db"))
	var stored string
	err := db.QueryRow(`SELECT value FROM config WHERE key = ?`, setupFingerprintKey).Scan(&stored)
	_ = db.Close()
	if err != nil || stored != fp {
		t.Fatalf("fingerprint in config: %q %v", stored, err)
	}
	if r := c.lore("setup"); !strings.Contains(r.stdout, "setup complete") {
		t.Fatalf("setup must be re-runnable: %s", r.stdout)
	}
	c.lore("memory", "add", "--body", "searchable after setup")
	if r := c.lore("search", "searchable"); !strings.Contains(r.stdout, "Found 1 hit for") {
		t.Fatalf("search index not working after setup: %s", r.stdout)
	}
}

// ── learn candidates ────────────────────────────────────────────────────

// addMemory adds a memory through the 0.1.18 core (`lore memory add`,
// ops/memory) and returns its id; old commands see it in lore.db after the
// lsync import of the row file.
func (c *clone) addMemory(body string) string {
	c.w.t.Helper()
	id := idPattern.FindString(c.lore("memory", "add", "--body", body).stdout)
	if id == "" {
		c.w.t.Fatal("memory add printed no id")
	}
	return id
}

// ── tier-3 repair ───────────────────────────────────────────────────────

func countRows(t *testing.T, path, q string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// ── support bundle ──────────────────────────────────────────────────────

func TestUC_MiscSupportBundleCounts(t *testing.T) {
	w, c := initProject(t)
	c.addMemory("one")
	c.addMemory("two")
	c.addRule("a rule")
	var b opssupportbundle.Bundle
	if err := json.Unmarshal([]byte(c.lore("support-bundle").stdout), &b); err != nil {
		t.Fatal(err)
	}
	if b.Counts["memories"] != 2 || b.Counts["rules"] != 1 || b.Counts["decisions"] != 0 || !b.Sanitized || b.IncludeContent {
		t.Fatalf("bundle: %+v", b)
	}
	for _, k := range []string{"hotfixes", "snapshots", "learn_candidates", "audit_log_rows", "render_history_rows"} {
		if _, ok := b.Counts[k]; !ok {
			t.Fatalf("count %q missing: %v", k, b.Counts)
		}
	}
	out := filepath.Join(t.TempDir(), "sub", "bundle.json")
	if r := c.lore("support-bundle", "--out", out); !strings.Contains(r.stdout, "wrote support bundle") {
		t.Fatalf("--out: %s", r.stdout)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal("bundle file not written")
	}
	// Outside any project the bundle still works, with no counts.
	r := w.run(t.TempDir(), nil, e2eBin, "support-bundle")
	var empty opssupportbundle.Bundle
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &empty) != nil || len(empty.Counts) != 0 {
		t.Fatalf("outside a project: %d %s %v", r.code, r.stderr, empty.Counts)
	}
}

// ── tag usage counts ────────────────────────────────────────────────────

func TestUC_MiscTagListUsageCounts(t *testing.T) {
	_, c := initProject(t)
	var tl struct {
		ID string `json:"id"`
	}
	decodeData(t, c.lore("tasklist", "add", "--title=t", "--body=b", "--json").stdout, &tl)
	t1 := idPattern.FindString(c.lore("task", "add", "open task", "--tasklist="+tl.ID, "--commitment=accepted").stdout)
	t2 := idPattern.FindString(c.lore("task", "add", "finished task", "--tasklist="+tl.ID, "--commitment=accepted").stdout)
	mem := c.addMemory("tagged memory")
	c.lore("tag", "add", "--name=backend")
	c.lore("tag", "add", "--name=infra")
	for table, id := range map[string]string{"tasks": t1, "memories": mem} {
		c.lore("tag", "attach", "--on-table="+table, "--on-id="+id, "--tag=backend")
	}
	c.lore("tag", "attach", "--on-table=tasks", "--on-id="+t2, "--tag=backend")
	c.lore("tag", "attach", "--on-table=memories", "--on-id="+mem, "--tag=infra")
	c.lore("task", "done", t2)
	out := c.lore("tag", "list").stdout
	if !regexp.MustCompile(`backend\s+\S+\s+\(2 active / 3 total\)`).MatchString(out) {
		t.Fatalf("a done task must not count as active:\n%s", out)
	}
	if !regexp.MustCompile(`infra\s+\S+\s+\(1 uses\)`).MatchString(out) {
		t.Fatalf("all-active tag:\n%s", out)
	}
	if r := c.loreAny("tag", "attach", "--on-table=tasks", "--on-id="+t1, "--tag=missing"); r.code == 0 || !strings.Contains(r.stderr, `tag "missing" not found`) {
		t.Fatalf("unknown tag: %d %s", r.code, r.stderr)
	}
}

// ── sync helpers ────────────────────────────────────────────────────────

// `lore sync conflicts` prints each saved version indented under its row.
func TestUC_MiscSyncConflictsTextIndents(t *testing.T) {
	_, c := initProject(t)
	mem := c.addRow("v1")
	// A restore no longer saves a conflict copy for a rule: rules are
	// core-owned in 0.1.18 and their row file always wins an adopt
	// (lsync owned.go, TestOwned_RestoreEveryPreferKeepsCoreFiles), so the
	// saved version a v0.1.17 restore left is written here as that adopt
	// wrote it, and the listing format is what is checked.
	seedConflictCopy(t, filepath.Join(c.dir, ".lore", "lore.db"), "rules", mem,
		"{\n  \"body\": \"v2 after backup\",\n  \"id\": \""+mem+"\"\n}")
	out := c.lore("sync", "conflicts").stdout
	if !strings.Contains(out, "rules/"+mem) {
		t.Fatalf("expected a saved version for %s:\n%s", mem, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "    ") && !strings.HasPrefix(l, "#") {
			t.Fatalf("saved document must be indented by four spaces:\n%s", out)
		}
	}
}

// seedConflictCopy saves one open conflict copy in lore.db the way an adopt
// pass does (lsync ListConflicts creates the bookkeeping table first).
func seedConflictCopy(t *testing.T, dbPath, table, id, otherDoc string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }() // test fixture; a close error has nothing to undo
	ctx := context.Background()
	if _, err := lsync.ListConflicts(ctx, db, false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO _lore_sync_conflicts (table_name, row_id, kept, other_doc, at) VALUES (?, ?, ?, ?, ?)`,
		table, id, string(lsync.KeptFile), otherDoc, "2026-11-04T10:00:00Z"); err != nil {
		t.Fatal(err)
	}
}

func TestUC_MiscIndent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, pad, want string }{
		{"a\nb", "  ", "  a\n  b"},
		{"a\nb\n\n", "> ", "> a\n> b"},
		{"one", "    ", "    one"},
		{"", "--", "--"},
	} {
		if got := indent(tc.in, tc.pad); got != tc.want {
			t.Fatalf("indent(%q,%q) = %q, want %q", tc.in, tc.pad, got, tc.want)
		}
	}
}

func TestUC_MiscDisplayPath(t *testing.T) {
	t.Parallel()
	root := filepath.Join(string(filepath.Separator), "repo", "proj")
	for _, tc := range []struct{ path, want string }{
		{filepath.Join(root, ".github", "workflows", "lore-sync-merge.yml"), ".github/workflows/lore-sync-merge.yml"},
		{root, "."},
		{filepath.Join(string(filepath.Separator), "elsewhere", "x.yml"), filepath.Join(string(filepath.Separator), "elsewhere", "x.yml")},
		{"", "lore-sync-merge workflow"},
	} {
		if got := displayPath(root, tc.path); got != tc.want {
			t.Fatalf("displayPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// abortCIMerge undoes an in-progress merge and hands back the original error.
func TestUC_MiscAbortCIMerge(t *testing.T) {
	dir := ciRepo(t, editReadme("# feature\n"), editReadme("# main\n"))
	head := headOf(t, dir)
	cmd := exec.Command("git", "merge", "--no-ff", "--no-commit", "main")
	cmd.Dir = dir
	if err := cmd.Run(); err == nil {
		t.Fatal("expected a conflicting merge")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal("setup: no merge in progress")
	}
	cause := errors.New("commit failed")
	if got := abortCIMerge(context.Background(), dir, cause); !errors.Is(got, cause) {
		t.Fatalf("must return the cause: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err == nil {
		t.Fatal("merge still in progress")
	}
	if headOf(t, dir) != head {
		t.Fatal("branch moved")
	}
	st := exec.Command("git", "status", "--porcelain")
	st.Dir = dir
	if out, _ := st.Output(); len(out) != 0 {
		t.Fatalf("work tree not restored: %s", out)
	}
	// With no merge in progress it still returns the cause.
	if got := abortCIMerge(context.Background(), dir, cause); !errors.Is(got, cause) {
		t.Fatalf("no merge in progress: %v", got)
	}
}
