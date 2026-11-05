package main

// Pass 2 [p2-oldcli] seams, through the real binary.

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// r3-targets (DECISION P2-OLDCLI-2): a tool file that carries lore's pointer
// but is not named by context.targets is left as it is, and render says so
// with the command that lists it, instead of skipping it silently.
func TestRender_UnlistedPointerFileIsNamed(t *testing.T) {
	r := newToolRepo(t, nil)
	r.lore("init")
	r.lore("render", "--target", "claude_code")
	r.mustWired("CLAUDE.md", "")
	before, _ := r.read("CLAUDE.md")
	r.lore("policy", "set", "context.targets", "codex", "--yes")
	out := r.lore("render").stdout
	want := "CLAUDE.md carries lore's pointer but context.targets does not name it, so render leaves it as is; list it with: lore policy set context.targets <profiles incl. claude_code> --yes"
	if !strings.Contains(out, want) {
		t.Fatalf("render must name the unlisted wired file:\n%s", out)
	}
	if after, _ := r.read("CLAUDE.md"); after != before {
		t.Fatalf("an unlisted file is left as it is:\nbefore %q\nafter  %q", before, after)
	}
	r.mustWired("AGENTS.md", "")
	// Listed again: no such line.
	r.lore("policy", "set", "context.targets", "codex,claude_code", "--yes")
	if out := r.lore("render").stdout; strings.Contains(out, "does not name it") {
		t.Fatalf("a listed file is not reported as unlisted:\n%s", out)
	}
}

// `lore repo set-url` with an address the normaliser refuses answers
// E_REPO_URL, a bad-argument error: exit 20 (round 2: exit 20 for every bad
// argument), and writes nothing.
func TestRepoSetURL_UnnormalisableIsExit20(t *testing.T) {
	r := newToolRepo(t, nil)
	r.lore("init")
	for _, bad := range []string{"not a url", "https://"} {
		res := r.w.run(r.dir, []string{"LORE_ACTOR=human:tools@example.com"}, e2eBin, "repo", "set-url", bad)
		if res.code != 20 || !strings.Contains(res.stderr, "E_REPO_URL") {
			t.Errorf("set-url %q: exit %d, stderr %s", bad, res.code, res.stderr)
		}
	}
}

// OV31 (P2-OLDSYNC-1): `lore trash purge --local --purged` drops this
// machine's safety copies of rows listed in the repo's own _purged.json. A
// destructive path: without --confirm it asks (E_NEEDS_CONFIRM, exit 26) and
// drops nothing; --dry-run lists; --confirm drops only listed ids; --local
// alone is a bad argument (E_INVALID_INPUT, exit 20).
func TestTrashPurgeLocalPurged(t *testing.T) {
	r := newToolRepo(t, nil)
	r.lore("init")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(r.dir, ".lore", "lore.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, id := range []string{"mem_gone1", "mem_keep1"} {
		if _, err := db.Exec(`INSERT INTO _lore_sync_trash(table_name, row_id, doc, reason, at) VALUES ('memories', ?, '{}', 'test', '2026-11-05T00:00:00Z')`, id); err != nil {
			t.Fatal(err)
		}
	}
	r.write(".lore/data/_purged.json", `{"_v": 1, "ids": ["mem_gone1"]}`+"\n")
	left := func() []string {
		t.Helper()
		rows, err := db.Query(`SELECT row_id FROM _lore_sync_trash ORDER BY row_id`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	run := func(args ...string) result {
		return r.w.run(r.dir, []string{"LORE_ACTOR=human:tools@example.com"}, e2eBin, append([]string{"trash", "purge"}, args...)...)
	}
	if res := run("--local"); res.code != 20 || !strings.Contains(res.stderr, "E_INVALID_INPUT") {
		t.Fatalf("--local without --purged: %d %s", res.code, res.stderr)
	}
	if res := run("--local", "--purged"); res.code != 26 || !strings.Contains(res.stderr, "E_NEEDS_CONFIRM") {
		t.Fatalf("no --confirm must ask: %d %s %s", res.code, res.stdout, res.stderr)
	}
	if got := left(); !slices.Equal(got, []string{"mem_gone1", "mem_keep1"}) {
		t.Fatalf("an unconfirmed purge dropped %v", got)
	}
	if res := run("--local", "--purged", "--dry-run"); res.code != 0 || !strings.Contains(res.stdout, "would drop 1") {
		t.Fatalf("dry run: %d %s %s", res.code, res.stdout, res.stderr)
	}
	if got := left(); len(got) != 2 {
		t.Fatalf("a dry run dropped rows: %v", got)
	}
	if res := run("--local", "--purged", "--confirm"); res.code != 0 || !strings.Contains(res.stdout, "dropped 1") {
		t.Fatalf("confirm: %d %s %s", res.code, res.stdout, res.stderr)
	}
	if got := left(); !slices.Equal(got, []string{"mem_keep1"}) {
		t.Fatalf("--confirm drops only the listed id: %v", got)
	}
	r.write(".lore/data/_purged.json", "{broken\n")
	if res := run("--local", "--purged", "--confirm"); res.code == 0 {
		t.Fatalf("an unreadable _purged.json must fail: %s", res.stdout)
	}
	if got := left(); !slices.Equal(got, []string{"mem_keep1"}) {
		t.Fatalf("a failed purge dropped %v", got)
	}
}
