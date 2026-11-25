//go:build bench

package recall

// set6_notinmemory_test.go — the recall gate's set-6 "not in memory" case
// (bug hunt H-233, devco D-974; Sync benchmark set 6 S6-01): on a store
// shaped like the benchmark's (an org folder of code_repo cards, a long
// as-built job-queue note whose duplicate-enqueue sentence sits in a
// section deep in it, many short notes sharing the question's common
// words), a question writing code identifiers (Queue.Enqueue,
// EnqueueOptions.DedupKey) or a second sentence starting with a capital
// never gets the "not in memory" hint while a returned row holds the
// asked terms, and the note answering it is in the top three with the
// answering sentence printed or its words named by the cut note
// (left_out). A question naming an identifier no row holds still gets
// the hint. Before the fix, ask printed "not in memory: no row holds
// queue.enqueue, enqueueoptions.dedupkey" above the row that answered.

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

// set6AbstainCase is one set-6 question: whether the hint is right, and
// for a held question the sentence that answers it.
type set6AbstainCase struct {
	id, question string
	abstain      bool
}

// set6AbstainCases are S6-01 as asked (identifiers), S6-01 in other words
// with a second sentence, and a control naming an identifier no row holds.
var set6AbstainCases = []set6AbstainCase{
	{"S6-01", "What does Queue.Enqueue return when EnqueueOptions.DedupKey matches a run that is not terminal?", false},
	{"S6-01b", "what does the job queue do with a dedup key while the first run is not terminal? Explain the run states.", false},
	{"S6-01c", "what does Queue.Reprioritize do with a waiting run", true},
}

// set6Answer is the sentence of the runq note that answers S6-01.
const set6Answer = "returns the first run's id"

// TestBenchSet6NotInMemory writes the set-6 shape and asks each
// set6AbstainCases question.
func TestBenchSet6NotInMemory(t *testing.T) {
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
	lore("memory", "add", "--to", "kb/org", "--create", "--body", "Folder kb/org holds the company's repositories.", "--yes")
	lore("resource", "kind", "add", "--kind", "code_repo", "--description", "A git repository of a product", "--to", "kb/org", "--yes")
	lore("resource", "field", "add", "--kind", "code_repo", "--field", "purpose", "--type", "text", "--to", "kb/org", "--yes")
	lore("memory", "add", "--to", "kb/app", "--create", "--body", "Folder kb/app holds the notes of the app.", "--yes")
	var b strings.Builder
	line := func(op string, in map[string]any) {
		raw, err := json.Marshal(map[string]any{"op": op, "input": in})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	for i := 1; i <= 20; i++ {
		line("resource add", map[string]any{"kind": "code_repo", "name": fmt.Sprintf("gizmo_svc_%02d", i), "to": "kb/org",
			"set": []string{fmt.Sprintf("purpose=The gizmo service %d: the job queue, its workers, runs and admin screens", i)}})
	}
	// the as-built job-queue note: sections sharing the common words, the
	// answering sentence deep in it
	var arch strings.Builder
	for s, head := range []string{"Overview", "Workers", "Retries", "Scheduling", "Run states", "Operations"} {
		fmt.Fprintf(&arch, "## %s\n\n", head)
		for p := range 4 {
			fmt.Fprintf(&arch, "Section %d part %d: the job queue keeps each run of a job in the queue table; a worker claims the run, heartbeats while it runs and records whether the run returned an error. ", s, p)
		}
		arch.WriteString("\n\n")
	}
	arch.WriteString("## Deduplication\n\nEnqueue takes options; when the dedup key of a new job matches a run that is not terminal yet (queued, claimed or running), Enqueue writes nothing and " + set6Answer + ". Once that run is terminal (done, dead or cancelled) the same key enqueues a new run.\n\n")
	for p := range 4 {
		fmt.Fprintf(&arch, "## Tables %d\n\nThe queue table keeps every run with its job kind, state and run-at time; finished runs older than 30 days are deleted by a nightly sweep (part %d).\n\n", p, p)
	}
	line("architecturenote add", map[string]any{"to": "kb/app", "title": "As built: runq, the background job queue", "body": arch.String()})
	for i := 1; i <= 60; i++ {
		line("memory add", map[string]any{"to": "kb/app", "body": fmt.Sprintf("Note %d: the job queue page lists each run of a job, its worker and the error it returned.", i)})
	}
	batch := filepath.Join(dir, "rows.jsonl")
	if err := os.WriteFile(batch, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	lore("batch", "apply", "--file", batch, "--yes")
	var misses []string
	for _, c := range set6AbstainCases {
		var out struct {
			Abstain struct {
				NotInMemory bool     `json:"not_in_memory"`
				Missing     []string `json:"missing"`
			} `json:"abstain"`
			Rows []struct {
				Title   string   `json:"title"`
				Body    string   `json:"body"`
				LeftOut []string `json:"left_out"`
			} `json:"rows"`
		}
		raw := lore("ask", "--query", c.question, "--json")
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("%s: ask --json: %v\n%s", c.id, err, raw)
		}
		text := lore("ask", "--query", c.question)
		if out.Abstain.NotInMemory != c.abstain || strings.HasPrefix(text, "not in memory") != c.abstain {
			misses = append(misses, fmt.Sprintf("%s %q: not in memory %v (missing %v), want %v:\n%s", c.id, c.question, out.Abstain.NotInMemory, out.Abstain.Missing, c.abstain, text))
			continue
		}
		if c.abstain {
			continue
		}
		found := false
		for _, r := range out.Rows[:min(3, len(out.Rows))] {
			if strings.Contains(r.Title, "runq") && (strings.Contains(r.Body, set6Answer) || slices.Contains(r.LeftOut, "dedup")) {
				found = true
			}
		}
		if !found {
			misses = append(misses, fmt.Sprintf("%s %q: the runq note is not in the top 3 with %q printed or dedup in left_out:\n%s", c.id, c.question, set6Answer, text))
		}
		t.Logf("%s: checked", c.id)
	}
	if len(misses) > 0 {
		t.Errorf("set-6 not-in-memory case missed %d:\n%s", len(misses), strings.Join(misses, "\n"))
	}
}
