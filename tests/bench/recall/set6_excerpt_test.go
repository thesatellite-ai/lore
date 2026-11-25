//go:build bench

package recall

// set6_excerpt_test.go — Sync benchmark set 6 ask 2 (bug hunt H-232,
// devco:D-973): when lore ask cuts a long note to its best sections, it
// keeps the section holding the question's subject (the words few rows
// of the store hold: duplicate, key, a type name, a list item), not the
// sections repeating its common words (job, run, first, return). S6-01's
// excerpt of a long job queue note dropped "a duplicate while the first
// run is non-terminal returns the first run's id"; S6-26 dropped point
// (6) of a numbered list. The store holds a long as-built note and eight
// short rows sharing its common words.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// set6Note is the long as-built job queue note: its "Workers" section
// repeats the questions' common words, "Caller API" holds the duplicate
// key sentence, "Run lifecycle" the EndCause list and "Write paths" a
// numbered list.
const set6Note = "# As built: job queue\n\n## Overview\n\nThe job queue runs every background job of the app. A job is a type and a payload; each attempt of a job is a run. The queue returns the run id to the caller, and the caller polls the run or waits for its webhook. Runs are kept for thirty days, then a sweep removes them with their logs.\n\n## Enqueue\n\nEnqueue checks that the job type is registered, writes the job row and its first run in one transaction, and returns the run id. The first run starts when a worker claims it; the worker claims the oldest run first, while newer runs wait. Enqueue returns an error when the payload is larger than 256 KB. A job enqueued with a delay gets its first run at the given time, and the run id is returned at once.\n\n## Workers\n\nWorkers claim runs with a lease of one minute and renew it while the run is still running. A run whose lease runs out is claimed again by the next worker and its attempt counter goes up. A worker runs at most eight runs at once. The first run of a job and every retry run go through the same claim, and the worker returns the run to the queue when it shuts down while the run is still running.\n\n## Caller API\n\nThe caller API takes the job type, the payload and an optional key. Payloads are JSON objects; a payload that is not an object is refused. A duplicate key while the earlier attempt is non-terminal gives back that attempt id and writes nothing; once it is terminal, a duplicate starts a new job. The API answers in under 50 ms because it only writes two rows.\n\n## Retries\n\nA failed run is retried with backoff (10 s, 1 min, 10 min) up to five times, then the job is marked dead. A retry is a new run of the same job; the first run keeps its error. A retry never duplicates side effects the job already committed: each job remembers what it wrote. Dead jobs are listed on the queue dashboard and can be retried by hand, which starts a new first run.\n\n## Run lifecycle\n\nA run is queued, then running, then ended. While a run is running its worker writes a heartbeat every ten seconds and its progress, and the first heartbeat marks it started. A run ends with a EndCause: Done, Failed, Cancelled, TimedOut or Superseded. A cancel request stops the run at its next checkpoint, and an ended run is never started again; a new run of the job is.\n\n## Operations\n\nOperators watch the queue depth and the oldest waiting run on the queue dashboard. The worker pool scales on queue depth. Pausing a job type leaves its runs in the queue until it is resumed; the key metric is the age of the oldest queued run.\n\n## Write paths\n\nThe job tables have four write paths:\n1. The caller API writes the job and its first run.\n2. The worker writes run progress and heartbeats.\n3. The retry timer writes each retry run.\n4. The archive sweep moves ended runs older than thirty days to cold storage."

// set6Rows are short rows that share the note's common words (job, run,
// first, return, while, still).
var set6Rows = []string{
	"in \"The export job returns the first run id to the caller and the caller polls it while the export is still running",
	"Report runs: the first run of a report job is slow because the cache is cold while it is still warming",
	"The billing job runs nightly; a run returns its totals to the ledger",
	"Mail job: each run sends at most 500 mails; the first run after a deploy warms the pool while older mails are still running",
	"The cleanup job runs hourly and returns the number of rows it removed",
	"Image job: a run resizes one upload; the first run of a new size fills the cache",
	"The search reindex job runs on deploy; its run returns when every shard is done",
	"Sync job: the first run of a new workspace imports everything, later runs only changes while the first is still running",
}

// set6ExcerptCases are the questions and the sentence each answer's cut
// of the note must print.
var set6ExcerptCases = []struct{ query, want string }{
	{"what does the job queue return for a duplicate key while the first run is still running", "A duplicate key while the earlier attempt is non-terminal gives back that attempt id"},
	{"what end causes can a job run end with", "Done, Failed, Cancelled, TimedOut or Superseded"},
	{"which write path moves ended runs to cold storage", "4. The archive sweep moves ended runs older than thirty days to cold storage."},
}

// TestSet6AskExcerptKeepsSubjectSection asks each case's question; the
// note must be cut and its cut must print the subject sentence.
func TestSet6AskExcerptKeepsSubjectSection(t *testing.T) {
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
	linkedRun(t, env, app, bin, "memory", "add", "--to", "kb/app", "--create", "--body", "The queue dashboard shows the depth of every job queue.", "--yes")
	linkedRun(t, env, app, bin, "architecturenote", "add", "--to", "kb/app", "--title", "As built: job queue", "--body", set6Note, "--meta", "fact_state=live", "--yes")
	for _, r := range set6Rows {
		linkedRun(t, env, app, bin, "memory", "add", "--to", "kb/app", "--body", r, "--yes")
	}
	for _, c := range set6ExcerptCases {
		out, _ := linkedRun(t, env, app, bin, "ask", "--query", c.query)
		if !strings.Contains(out, "lore show --id") {
			t.Errorf("%q: the note was not cut (the case needs a cut):\n%s", c.query, out)
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("%q: the cut dropped the subject sentence (%q):\n%s", c.query, c.want, out)
		}
	}
}
