//go:build bench

package heldout

// bench_test.go — task bench:heldout and task bench:heldout:dump (H-237,
// devco D-978). TestBenchHeldout writes the generated store with a real
// lore (LORE_BENCH_BIN, else a build of ./cmd/lore), asks every question of
// LORE_HELDOUT_FILE (default tune.jsonl) with `lore ask --query`, as the
// benchmark's answerer does, and writes the report (LORE_HELDOUT_REPORT,
// default <repo>/tmp/bench/heldout.md). It is report-only: it fails on a
// bad question file or a broken setup, never on the score.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/bench/recall"
)

// benchNow pins lore's "now" so dates in the answers never move.
var benchNow = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestHeldoutDump writes the store's dump for the question writer into
// LORE_HELDOUT_DUMP (task bench:heldout:dump sets it).
func TestHeldoutDump(t *testing.T) {
	dir := os.Getenv("LORE_HELDOUT_DUMP")
	if dir == "" {
		t.Skip("LORE_HELDOUT_DUMP not set")
	}
	if !filepath.IsAbs(dir) { // task runs from the repo root, go test from this folder
		dir = filepath.Join(repoRoot(t), dir)
	}
	s := Generate(DefaultSeed)
	if err := s.WriteDump(dir); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d rows and %d cards to %s", len(s.Rows), len(s.Cards), dir)
}

// run runs one command in dir with env and fails the test on an error.
func run(t *testing.T, env []string, dir, bin string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(bin), strings.Join(args, " "), err, out)
	}
	return out
}

// TestBenchHeldout writes the store and scores lore ask on the questions.
func TestBenchHeldout(t *testing.T) {
	root := repoRoot(t)
	file := os.Getenv("LORE_HELDOUT_FILE")
	if file == "" {
		file = filepath.Join(root, "tests", "bench", "heldout", "tune.jsonl")
	} else if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	s := Generate(DefaultSeed)
	qs, err := LoadQuestionFile(file, s)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := os.Getenv("LORE_BENCH_BIN")
	if bin == "" {
		bin = filepath.Join(dir, "bin", "lore")
		build := exec.Command("go", "build", "-o", bin, "./cmd/lore")
		build.Dir, build.Env = root, append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build lore: %v\n%s", err, out)
		}
	}
	bin, _ = filepath.Abs(bin)
	home, app, srv := filepath.Join(dir, "home"), filepath.Join(dir, "app"), filepath.Join(dir, "srv")
	for _, d := range []string{home, app, srv} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = Ada Test\n\temail = ada@example.test\n[init]\n\tdefaultBranch = main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := os.Getenv("TMPDIR")
	if tmp == "" {
		tmp = os.TempDir()
	}
	env := recall.HarnessEnv(home, filepath.Dir(bin), tmp, benchNow)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	keyOf := writeStore(t, s, env, bin, git, app, srv, dir)
	t.Logf("store written: %d rows, %d cards; %d questions from %s", len(s.Rows), len(s.Cards), len(qs), file)

	var rs []Result
	for _, q := range qs {
		raw := run(t, env, app, bin, "ask", "--query", q.Question, "--json")
		a, err := ParseAnswer(raw)
		if err != nil {
			t.Fatalf("%s: %v\n%s", q.ID, err, raw)
		}
		r := Score(q, a, keyOf, s)
		start := time.Now() // warm: the same question again, as text
		run(t, env, app, bin, "ask", "--query", q.Question)
		r.Warm = time.Since(start)
		rs = append(rs, r)
	}
	report := os.Getenv("LORE_HELDOUT_REPORT")
	if report == "" {
		report = filepath.Join(root, "tmp", "bench", "heldout.md")
	}
	if err := os.MkdirAll(filepath.Dir(report), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report, []byte(Report(file, s.Seed, rs)), 0o600); err != nil {
		t.Fatal(err)
	}
	fmt.Println(Summarize(rs).Line())
	t.Logf("report: %s", report)
}

// writeStore writes the store into a store "kb" attached to the code repo
// app and returns the bench_key of every row id.
func writeStore(t *testing.T, s *Store, env []string, bin, git, app, srv, dir string) map[string]string {
	t.Helper()
	lore := func(args ...string) []byte { return run(t, env, app, bin, args...) }
	run(t, env, app, git, "init", "-q")
	run(t, env, app, git, "commit", "-q", "--allow-empty", "-m", "init")
	lore("init", "--yes")
	bare := filepath.Join(srv, StoreName+".git")
	run(t, env, srv, git, "init", "-q", "--bare", "-b", "main", bare)
	lore("store", "init", "--name", StoreName, "--url", bare, "--yes")
	for _, f := range []string{AppFolder, OtherFolder} {
		lore("store", "folder", "add", "--store", StoreName, "--folder", strings.TrimPrefix(f, StoreName+"/"))
	}
	lore("resource", "kind", "add", "--kind", "code_repo", "--description", "A git repository of a product", "--to", OrgFolder, "--yes")
	for _, f := range []string{"purpose", "provider", "url"} {
		lore("resource", "field", "add", "--kind", "code_repo", "--field", f, "--type", "text", "--to", OrgFolder, "--yes")
	}
	for _, c := range s.Cards {
		lore("resource", "add", "--kind", "code_repo", "--name", c.Name, "--set", "purpose="+c.Purpose,
			"--set", "provider="+c.Provider, "--set", "url="+c.URL, "--to", OrgFolder, "--yes")
	}
	for _, f := range []string{AppFolder, OtherFolder} {
		raw, err := s.Batch(f)
		if err != nil {
			t.Fatal(err)
		}
		batch := filepath.Join(dir, "rows.jsonl")
		if err := os.WriteFile(batch, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		lore("batch", "apply", "--file", batch, "--yes")
	}
	var metas []struct {
		ID  string `json:"entity_id"`
		Key string `json:"value_json"`
	}
	out := lore("sql", "run", "--format", "json", "--query", "SELECT entity_id, value_json FROM row_meta_all WHERE key_name = '"+MetaKey+"' AND value_json IS NOT NULL ORDER BY entity_id")
	if err := json.Unmarshal(out, &metas); err != nil {
		t.Fatalf("bench_key map: %v\n%s", err, out)
	}
	keyOf := map[string]string{}
	for _, m := range metas { // the store is read under two sources (kb and its folder); one copy has the value
		if m.Key != "" {
			keyOf[m.ID] = m.Key
		}
	}
	if len(keyOf) != len(s.Rows) {
		t.Fatalf("lore holds %d rows with a bench_key, the store has %d", len(keyOf), len(s.Rows))
	}
	return keyOf
}
