//go:build bench

package recall

// set6_shortexact_test.go — the recall gate's set-6 short-exact-row case
// (bug hunt H-231, devco D-972; Sync benchmark set 6 item 1: S6-06, 09,
// 10, 18, 24, 32, 33, 34): on a store of several-KB as-built notes that
// each hold the store's common words (sync, cli, commands, workflow, run,
// key, file, worker) in sections of their own, the short memory or
// hotfix naming the asked command, flag or error is ranked first by lore
// search and must be in lore ask's top three. Before the fix a long note
// ranked by every word its whole text held, plus the common words of its
// best section's heading, and three notes sharing only common words
// stood above the memory naming "wkf trigger / reconcile / schedule".
//
// Invariant: for these shapes, search's first row is in ask's top three.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// set6ExactCase is one set-6 question and the text its answering row holds.
type set6ExactCase struct {
	id, question, answer string
}

var set6ExactCases = []set6ExactCase{
	{"S6-06", "which tool cli commands trigger, reconcile or schedule a workflow run", "wkf trigger / reconcile / schedule"},
	{"S6-32", "which worker command requeues the mirror of a file stuck in the failed mirror state", "files resync"},
	{"S6-34", "which command rotates the encryption key of stored credentials with vault-rekey and what guards it", "worker vault-rekey"},
	{"S6-18", "what does the default category import seed do, is it set-when-blank", "set-when-blank"},
}

// TestBenchSet6ShortExact writes the set-6 short-exact-row shape and asks
// each set6ExactCases question through lore search and lore ask.
func TestBenchSet6ShortExact(t *testing.T) {
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
	if err := os.WriteFile(batch, []byte(set6ExactRows(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	lore("batch", "apply", "--file", batch, "--yes")
	var misses []string
	for _, c := range set6ExactCases {
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
		top := asked.Rows[:min(3, len(asked.Rows))]
		var ids []string
		answered := false
		for _, r := range top {
			ids = append(ids, r.ID)
			answered = answered || strings.Contains(r.Title+" "+r.Body, c.answer)
		}
		if !answered {
			misses = append(misses, fmt.Sprintf("%s %q: no row holding %q in ask's top three:\n%s", c.id, c.question, c.answer, lore("ask", "--query", c.question)))
		}
		if len(found.Hits) > 0 && !slices.Contains(ids, found.Hits[0].ID) {
			misses = append(misses, fmt.Sprintf("%s %q: search's first row %s is not in ask's top three %v", c.id, c.question, found.Hits[0].ID, ids))
		}
		t.Logf("%s: ask top three %v, search first %v", c.id, ids, found.Hits[:min(1, len(found.Hits))])
	}
	if len(misses) > 0 {
		t.Errorf("set-6 short-exact case missed %d time(s):\n%s", len(misses), strings.Join(misses, "\n"))
	}
}

// set6ExactRows is the batch file of the shape: as-built notes, rows
// sharing the common words, filler, and the short rows that answer.
func set6ExactRows(t *testing.T) string {
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
	filler := "The parcel desk prints zebra labels at the dock and carriers collect parcels twice daily. Theme colours come from the brand palette; the admin screens use the compact density."
	note := func(title string, own ...string) {
		var body strings.Builder
		body.WriteString("# " + title + "\n\n")
		for _, s := range own {
			head, text, _ := strings.Cut(s, "|")
			body.WriteString("## " + head + "\n\n" + text + "\n\n")
		}
		body.WriteString("## Tool cli commands\n\nThe tool cli has a command per workflow; each command prints the run it started and the worker that took it.\n\n")
		body.WriteString("## Stored keys and tables\n\nEach run is stored in a table with its key; a failed run keeps its row so the worker can read it on the next run.\n\n")
		body.WriteString("## Build and import\n\nThe build step imports each file and category from the feed, writes the table and logs the worker run.\n\n")
		for n := 1; n <= 14; n++ {
			fmt.Fprintf(&body, "## Notes %d\n\n%s\n\n", n, filler)
		}
		line("architecturenote add", map[string]any{"to": "kb/app", "title": title, "body": body.String(), "meta": []string{"fact_state=live", "fact_verified_at=2026-11-20"}})
	}
	note("As built: file manager", "Upload path|A file upload goes to the upload handler, which writes the file to object storage.", "Mirror uploads|After an upload a worker job copies the file to the mirror bucket; a failed mirror job leaves the file in the failed mirror state.")
	note("As built: cron binary", "Schedules|The cron binary reads each schedule from the store and starts a workflow run when it is due.", "Callbacks|A finished workflow run posts its result to the callback of the workflow.")
	note("As built: auth manager", "Stored credentials|Partner credentials are stored encrypted in the credentials table with a key version.", "Signing keys|Tokens are signed with a key from the key ring; the key ring rotates monthly.")
	note("As built: key-value store", "Namespaces|Each workspace has its own namespace of keys.", "Expiry|The sweeper removes expired keys every minute.")
	note("As built: feed builder", "Feed import|The feed builder imports each supplier feed into the catalog.", "Categories|The category import reads the category tree of the shop and writes the category rows.")
	note("As built: workflow engine", "Run lifecycle|A workflow run moves from queued to running to done; the engine schedules each step.", "Timers|A timer step waits until its schedule fires, then the run goes on.")
	note("As built: job queue", "Enqueue|A job is enqueued with a kind and a payload.", "Retries|A failed job is retried with backoff up to five times.")
	line("decision add", map[string]any{"to": "kb/app", "title": "Files: one mirror job per file", "body": "Each uploaded file gets exactly one mirror job; the worker never runs two mirror jobs for the same file, so a failed job is retried by the same job."})
	line("decision add", map[string]any{"to": "kb/app", "title": "Categories come from the shop", "body": "The category tree of the shop is the source; the category import never invents a category."})
	for _, c := range []string{
		"The worker command logs each file it reads.", "A tool cli command prints the workflow run it started.",
		"Each workflow run is stored in the run table with its key.", "The import worker reads the feed and logs each category.",
		"The worker restarts when a command fails; the run is kept.", "The sync worker reads each workflow from the table.",
		"The cli prints each stored key of the workspace.", "The category import command runs once per workspace.",
	} {
		line("memory add", map[string]any{"to": "kb/app", "body": c})
	}
	for i := 1; i <= 20; i++ {
		line("memory add", map[string]any{"to": "kb/app", "body": fmt.Sprintf("Note %d: the dashboard lists each workspace with its plan, owner and the last deploy.", i)})
	}
	line("memory add", map[string]any{"to": "kb/app", "body": "tool cli wkf trigger / reconcile / schedule: trigger starts one workflow run now, reconcile re-checks stuck runs, schedule prints the next due run."})
	line("memory add", map[string]any{"to": "kb/app", "body": "worker files resync --file <id> requeues the mirror job of one file stuck in the failed state."})
	line("hotfix add", map[string]any{"to": "kb/app", "title": "vault-rekey re-encrypts stored credentials", "body": "worker vault-rekey re-encrypts every stored credential under the newest key version; it refuses to run without --confirm and while another rekey holds the lock."})
	line("memory add", map[string]any{"to": "kb/app", "body": "The default category-import seed is set-when-blank: it writes the default categories only when the workspace has none."})
	return b.String()
}
