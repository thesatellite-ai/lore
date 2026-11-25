//go:build bench

package recall

// set6_forms_test.go — the recall gate's set-6 word-form case (bug hunt
// H-235, devco D-976; Sync benchmark set 6 item 5: off-topic first hits
// S6-05, S6-28, S6-34 and the sibling questions S6-27/28/29): a question
// writing a word in another form than the answering row (validate for
// validation, enqueuing for enqueue, jobs for job, encrypted for
// re-encrypts) next to rows about something else that write the
// question's other words in its own form. lore search stems both and
// lists the answering row first; before the fix lore ask matched only
// exact and prefix forms, so the off-topic row stood first and sibling
// questions got different rows.
//
// Invariant: for these shapes, ask's first row is search's first row,
// the one that answers.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// set6FormCase is one set-6 question and the text its answering row holds.
type set6FormCase struct {
	id, question, answer string
}

var set6FormCases = []set6FormCase{
	{"S6-27", "what does a catalog validation rule hold", "Catalog validation"},
	{"S6-28", "which product fields and rules does the catalog validate", "Catalog validation"},
	{"S6-29", "how are catalog validations of a field run", "Catalog validation"},
	{"S6-05", "what does the gate check before enqueuing jobs", "The enqueue gate"},
	{"S6-34", "which worker command encrypted the stored credentials again", "worker vault-rekey"},
}

// TestBenchSet6WordForms writes the set-6 word-form shape and asks each
// set6FormCases question through lore search and lore ask.
func TestBenchSet6WordForms(t *testing.T) {
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
	lore := func(args ...string) string { out, _ := linkedRun(t, env, app, bin, args...); return out }
	linkedRun(t, env, app, git, "init", "-q")
	linkedRun(t, env, app, git, "commit", "-q", "--allow-empty", "-m", "init")
	lore("init", "--yes")
	linkedRun(t, env, srv, git, "init", "-q", "--bare", "-b", "main", filepath.Join(srv, "kb.git"))
	lore("store", "init", "--name", "kb", "--url", filepath.Join(srv, "kb.git"), "--yes")
	lore("memory", "add", "--to", "kb/app", "--create", "--body", "Folder kb/app holds the notes of the app.", "--yes")
	batch := filepath.Join(dir, "rows.jsonl")
	if err := os.WriteFile(batch, []byte(set6FormRows(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	lore("batch", "apply", "--file", batch, "--yes")
	var misses []string
	for _, c := range set6FormCases {
		var found struct {
			Hits []struct {
				ID string `json:"id"`
			} `json:"hits"`
		}
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		raw := lore("search", "--query", c.question, "--json")
		if err := json.Unmarshal([]byte(raw), &env); err != nil || json.Unmarshal(env.Data, &found) != nil {
			t.Fatalf("%s: search --json: %v\n%s", c.id, err, raw)
		}
		var asked struct {
			Rows []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"rows"`
		}
		raw = lore("ask", "--query", c.question, "--json")
		if err := json.Unmarshal([]byte(raw), &asked); err != nil {
			t.Fatalf("%s: ask --json: %v\n%s", c.id, err, raw)
		}
		var ids []string
		for _, r := range asked.Rows[:min(3, len(asked.Rows))] {
			ids = append(ids, r.ID)
		}
		if len(asked.Rows) == 0 || !strings.Contains(asked.Rows[0].Title+" "+asked.Rows[0].Body, c.answer) {
			misses = append(misses, fmt.Sprintf("%s %q: ask's first row does not hold %q:\n%s", c.id, c.question, c.answer, lore("ask", "--query", c.question)))
		}
		if len(found.Hits) > 0 && (len(ids) == 0 || ids[0] != found.Hits[0].ID) {
			misses = append(misses, fmt.Sprintf("%s %q: search's first row %s is not ask's first row (top three %v)", c.id, c.question, found.Hits[0].ID, ids))
		}
		t.Logf("%s: ask top three %v, search first %v", c.id, ids, found.Hits[:min(1, len(found.Hits))])
	}
	if len(misses) > 0 {
		t.Errorf("set-6 word-form case missed %d time(s):\n%s", len(misses), strings.Join(misses, "\n"))
	}
}

// set6FormRows is the batch file of the shape: the answering rows, rows
// about something else sharing the questions' other words in their own
// form, and filler.
func set6FormRows(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	line := func(op string, in map[string]any) {
		raw, err := json.Marshal(map[string]any{"op": op, "input": in})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	line("rule add", map[string]any{"to": "kb/app", "body": "One writer per catalog table: only the catalog worker writes product fields and rules; other services read them."})
	line("architecturenote add", map[string]any{"to": "kb/app", "title": "Attribute value writer", "body": "The attribute value writer runs sync jobs per product; a gate skips the jobs of a product whose values did not change.", "meta": []string{"fact_state=live", "fact_verified_at=2026-11-20"}})
	line("hotfix add", map[string]any{"to": "kb/app", "title": "Worker restart keeps stored credentials", "body": "The worker restarts when a job fails; the stored credentials are kept, so the next worker run goes on."})
	for _, c := range []string{
		"The catalog worker logs each product it reads.", "A sync job prints the product it wrote.",
		"Each product field is stored with its value.", "The worker reads each catalog table.",
		"The job queue keeps each job for a week.", "Stored credentials are read by the worker.",
		"A product has a title, a price and its fields.", "The catalog sync runs every hour.",
	} {
		line("memory add", map[string]any{"to": "kb/app", "body": c})
	}
	for i := 1; i <= 20; i++ {
		line("memory add", map[string]any{"to": "kb/app", "body": fmt.Sprintf("Note %d: the dashboard lists each workspace with its plan, owner and the last deploy.", i)})
	}
	line("memory add", map[string]any{"to": "kb/app", "body": "Catalog validation: each validation rule holds a field, an operator and a severity; an error severity blocks the publish, a warning only shows."})
	line("memory add", map[string]any{"to": "kb/app", "body": "The enqueue gate refuses a job while the same job key is still queued and returns the id of the first job."})
	line("hotfix add", map[string]any{"to": "kb/app", "title": "vault-rekey", "body": "worker vault-rekey re-encrypts every stored credential under the newest key version; it refuses to run without --confirm and while another rekey holds the lock."})
	return b.String()
}
