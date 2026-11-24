//go:build bench

package recall

// set5_excerpt_test.go — Sync benchmark set 5 ask 3 (bug hunt H-229,
// devco:D-970): when lore ask cuts a row to fit its answer, the excerpt
// keeps the sentence that holds the question's identifier. S5-26's
// excerpt of a long as-built architecture note dropped the only sentence
// naming the caller of the asked function: its paragraphs repeated the
// question's common words and the cut kept those. The case asks one
// question per kind ask cuts (an architecture note with ## sections, a
// memory, a decision, a hotfix), each naming an identifier that one
// sentence deep in the row holds, at a --max-chars that cuts the row.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// set5Filler is a paragraph that repeats the questions' common words
// (stock, sync, warehouse, import, retry) and names no identifier.
const set5Filler = "The stock sync of each warehouse reads the import inbox in batches and writes the stock table per workspace, warehouse after warehouse, and a failed import row is kept for the next sync retry of that warehouse. "

// set5ExcerptCase is one row of the case: its kind, the add command's
// arguments, the question and the sentence its answer must print.
type set5ExcerptCase struct {
	kind  string
	add   []string
	query string
	want  string
}

func set5ExcerptCases() []set5ExcerptCase {
	f := strings.Repeat(set5Filler, 3)
	note := "# As built: warehouse stock sync\n\n## Overview\n\n" + f + "\n\n## Write path\n\n" + f + "\n\n" + f + "\n\n" + f + "\n\nBatches are claimed with a two-minute lease. The only caller of applyLevelDelta is the nightly reconcile job in jobs/reconcile.go. Leases are swept hourly.\n\n## Operations\n\n" + f
	return []set5ExcerptCase{
		{"architecturenote", []string{"architecturenote", "add", "--title", "As built: warehouse stock sync", "--body", note, "--meta", "fact_state=live"},
			"who calls applyLevelDelta in the warehouse stock sync", "The only caller of applyLevelDelta is the nightly reconcile job"},
		{"memory", []string{"memory", "add", "--body", f + "An import row older than the newest stock change stops with ErrStaleStockRow and is fetched again. " + f},
			"when does the warehouse stock import stop with ErrStaleStockRow", "stops with ErrStaleStockRow and is fetched again"},
		{"decision", []string{"decision", "add", "--title", "Warehouse sync retries whole batches", "--body", f + "The batch size is set by MAX_SYNC_BATCH, 200 rows, chosen so one batch commits in under a second. " + f},
			"what is MAX_SYNC_BATCH for the warehouse stock sync", "The batch size is set by MAX_SYNC_BATCH, 200 rows"},
		{"hotfix", []string{"hotfix", "add", "--title", "Warehouse sync stuck on one warehouse", "--body", f + "Fix: purgeStockLease drops a lease left by a crashed replica; run it once per stuck warehouse. " + f},
			"how do I use purgeStockLease when the warehouse stock sync is stuck", "purgeStockLease drops a lease left by a crashed replica"},
	}
}

// TestSet5AskExcerptKeepsIdentifierSentence asks each case's question at a
// --max-chars that cuts its row; the row must lead and its excerpt must
// print the sentence naming the identifier.
func TestSet5AskExcerptKeepsIdentifierSentence(t *testing.T) {
	dir := t.TempDir()
	bin := os.Getenv(envBin)
	if bin == "" {
		bin = filepath.Join(dir, "bin", "lore")
		build := exec.Command("go", "build", "-o", bin, "./cmd/lore")
		build.Dir, build.Env = repoRoot(t), append(os.Environ(), "CGO_ENABLED=0")
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
	env := HarnessEnv(home, filepath.Dir(bin), tmp, time.Time{})
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	linkedRun(t, env, srv, git, "init", "-q", "--bare", "-b", "main", filepath.Join(srv, "kb.git"))
	linkedRun(t, env, app, git, "init", "-q")
	linkedRun(t, env, app, git, "commit", "-q", "--allow-empty", "-m", "init")
	linkedRun(t, env, app, bin, "init", "--yes")
	linkedRun(t, env, app, bin, "store", "init", "--name", "kb", "--url", filepath.Join(srv, "kb.git"), "--yes")
	linkedRun(t, env, app, bin, "memory", "add", "--to", "kb/app", "--create", "--body", "The warehouse dashboard shows the sync lag per warehouse.", "--yes")
	cases := set5ExcerptCases()
	for _, c := range cases {
		linkedRun(t, env, app, bin, append(c.add, "--to", "kb/app", "--yes")...)
	}
	for _, c := range cases {
		out, _ := linkedRun(t, env, app, bin, "ask", "--query", c.query, "--max-chars", "700")
		if !strings.Contains(out, "lore show --id") {
			t.Errorf("%s: %q: the row was not cut (the case needs a cut):\n%s", c.kind, c.query, out)
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: %q: the excerpt dropped the sentence naming the identifier (%q):\n%s", c.kind, c.query, c.want, out)
		}
	}
}
