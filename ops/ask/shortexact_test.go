package ask

// shortexact_test.go — devco D-972 (bug hunt H-231, Sync benchmark set 6
// item 1): a short memory or hotfix naming the asked command, flag or
// error (files resync, vault-rekey, set-when-blank, disallowed
// callback hosts) ranked first in lore search, but lore ask showed long
// architecture notes and decisions sharing only the question's common
// words and left the short row out. A long row now ranks by its best
// section's words alone (not by every word its whole text holds), and a
// question word few rows hold counts more than one most rows share.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// set6Case is one set-6 shaped question: the rows, and the short row
// that answers it (the row lore search ranks first).
type set6Case struct {
	name, q, want string
	rows          []textRow
}

// set6Background is the store the set-6 questions are asked of: long
// as-built notes and decisions that share the questions' common words
// (file, job, worker, command, run, key, sync, store), and filler rows so
// a word's spread reads like a real store's.
func set6Background(at time.Time) []textRow {
	// every as-built note also holds the store's common words in sections
	// of their own, as a several-KB note of a real store does: its whole
	// text holds most of any question's common words
	ops := [][2]string{
		{"Tool cli commands", "The tool cli has a command per workflow; each command prints the run it started and the worker that took it."},
		{"Stored keys and tables", "Each run is stored in a table with its key; a failed run keeps its row so the worker can read it on the next run."},
		{"Build and import", "The build step imports each file and category from the feed, writes the table and logs the worker run."},
	}
	note := func(id, title string, secs ...[2]string) textRow {
		return textRow{Table: "architecture_notes", ID: id, Place: "kb/app", Title: title, Body: longNote(title, 3600, append(secs, ops...)...), At: at, FactState: "live", Verified: "2026-11-20"}
	}
	dec := func(id, title, body string, h int) textRow {
		return textRow{Table: "decisions", ID: id, Place: "kb/app", Title: title, Body: body, At: at.Add(time.Duration(h) * time.Hour)}
	}
	rows := []textRow{
		note("anr_files", "As built: file manager",
			[2]string{"Upload path", "A file upload goes to the upload handler, which writes the file to object storage and records a file row; a failed upload leaves no row."},
			[2]string{"Mirror uploads", "After an upload a worker job copies the file to the mirror bucket. The mirror job retries three times; a failed mirror job leaves the file in the failed mirror state and an alert fires."},
			[2]string{"Commands", "The worker binary has commands for file maintenance: files verify checks hashes, files gc removes orphans. Each command takes a workspace flag."},
		),
		note("anr_cron", "As built: cron binary",
			[2]string{"Schedules", "The cron binary reads each schedule from the store and starts a workflow run when it is due; a run that is still running is skipped."},
			[2]string{"Commands", "The cron binary has a tool cli with commands to list schedules and print the next run of each workflow."},
			[2]string{"Callbacks", "A finished workflow run posts its result to the callback of the workflow; the host of each callback comes from the workspace settings."},
		),
		note("anr_auth", "As built: auth manager",
			[2]string{"Stored credentials", "Partner credentials are stored encrypted in the credentials table; each row holds the ciphertext and the key version used."},
			[2]string{"Signing keys", "Tokens are signed with a key from the key ring; the key ring rotates monthly and keeps the last two keys."},
			[2]string{"Commands", "The admin command group lists sessions and prints the key ring; each command needs the admin role."},
		),
		note("anr_feed", "As built: feed builder",
			[2]string{"Feed import", "The feed builder imports each supplier feed into the catalog; a product edited by hand keeps the hand edit and the feed value is dropped."},
			[2]string{"Categories", "Each product maps to one category; the category import reads the category tree of the shop and writes the category rows."},
			[2]string{"Manifest", "Each build writes a manifest of the feeds it read; the manifest lists every feed and its row count."},
		),
		note("anr_metrics", "As built: metrics rollup",
			[2]string{"Rollups", "Each hour the rollup job sums the events of the hour into the rollup table, one row per workspace and metric."},
			[2]string{"Workers", "Several workers read the event stream; each worker writes its own partial counts, merged by the rollup job."},
		),
		note("anr_kv", "As built: key-value store",
			[2]string{"Namespaces", "Each workspace has its own namespace of keys; a key holds one value and a version."},
			[2]string{"Expiry", "A key may expire; the sweeper removes expired keys every minute."},
		),
		note("anr_jobs", "As built: job queue",
			[2]string{"Enqueue", "A job is enqueued with a kind and a payload; a duplicate job while the first is running returns the first job."},
			[2]string{"Retries", "A failed job is retried with backoff up to five times, then marked dead."},
		),
		dec("dec_files1", "Files: one mirror job per file", "Each uploaded file gets exactly one mirror job; the worker never runs two mirror jobs for the same file, so a failed job is retried by the same job.", 1),
		dec("dec_files2", "Files: object storage is the source of truth", "The file row only points at the object; the worker command that repairs a file reads object storage first.", 2),
		dec("dec_files3", "Files: uploads are idempotent by hash", "A second upload of the same file in a workspace returns the first file row; the job queue sees one job.", 3),
		dec("dec_cat1", "Categories come from the shop", "The category tree of the shop is the source; the category import never invents a category.", 4),
		dec("dec_cat2", "Category rows are per workspace", "Each workspace keeps its own category rows; an import writes only the workspace's own rows.", 5),
		dec("dec_feed1", "Hand edits win over the feed", "A product edited by hand keeps the edit; the next feed import does not overwrite it.", 6),
	}
	// rows sharing two or three of the common words each, as a real
	// store's memories and tasks do
	common := []string{
		"The worker command logs each file it reads.",
		"A tool cli command prints the workflow run it started.",
		"Each workflow run is stored in the run table with its key.",
		"The import worker reads the feed and logs each category.",
		"A build writes its table rows in one transaction per file.",
		"The cli prints each stored key of the workspace.",
		"The worker restarts when a command fails; the run is kept.",
		"Each feed import run writes a table row with the build time.",
		"Uploads of a file keep their key in the file table.",
		"The sync worker reads each workflow from the table.",
		"The category import command runs once per workspace.",
		"A stored file is read by the worker on each run.",
	}
	for i, c := range common {
		rows = append(rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_common%02d", i), Place: "kb/app", Body: c, At: at.Add(-time.Duration(30+i) * time.Hour)})
	}
	for i := range 24 {
		rows = append(rows, textRow{Table: "memories", ID: fmt.Sprintf("mem_fill%02d", i), Place: "kb/app",
			Body: fmt.Sprintf("Note %d: the dashboard lists each workspace with its plan, owner and the last deploy; the billing page shows invoices per month.", i), At: at.Add(-time.Duration(i) * time.Hour)})
	}
	return rows
}

// set6Cases are the set-6 item-1 questions (S6-06, 09, 10, 18, 24, 32,
// 33, 34) on synthetic rows of the same shape: each answer is a short
// memory or hotfix naming the asked command, flag or error.
func set6Cases() []set6Case {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	mem := func(id, body string) textRow {
		return textRow{Table: "memories", ID: id, Place: "kb/app", Body: body, At: at.Add(-48 * time.Hour)}
	}
	hfx := func(id, title, body string) textRow {
		return textRow{Table: "hotfixes", ID: id, Place: "kb/app", Title: title, Body: body, At: at.Add(-48 * time.Hour)}
	}
	return []set6Case{
		{name: "S6-06", q: "which tool cli commands trigger, reconcile or schedule a workflow run",
			want: "mem_wfr", rows: []textRow{mem("mem_wfr", "tool cli wkf trigger / reconcile / schedule: trigger starts one workflow run now, reconcile re-checks stuck runs, schedule prints the next due run.")}},
		{name: "S6-09", q: "which callback hosts are disallowed for a workflow callback",
			want: "mem_hosts", rows: []textRow{mem("mem_hosts", "Disallowed callback hosts: localhost, link-local and private ranges; registering one returns ErrCallbackHostDisallowed.")}},
		{name: "S6-10", q: "what happens when the manifest build errors, is an empty manifest written",
			want: "mem_manifest", rows: []textRow{mem("mem_manifest", "Manifest build errors are returned to the caller; an empty manifest is never written.")}},
		{name: "S6-18", q: "what does the default category import seed do, is it set-when-blank",
			want: "mem_seed", rows: []textRow{mem("mem_seed", "The default category-import seed is set-when-blank: it writes the default categories only when the workspace has none.")}},
		{name: "S6-24", q: "what happens to a conflict between a feed import and a hand edit, is it a review item",
			want: "mem_conflict", rows: []textRow{mem("mem_conflict", "Feed import conflicts are review items: a conflict between the feed and a hand edit opens a review item instead of overwriting.")}},
		{name: "S6-32", q: "which worker command requeues the mirror of a file stuck in the failed mirror state",
			want: "mem_requeue", rows: []textRow{mem("mem_requeue", "worker files resync --file <id> requeues the mirror job of one file stuck in the failed state.")}},
		{name: "S6-33", q: "which worker is the single writer of the rollup table",
			want: "hfx_rollup", rows: []textRow{hfx("hfx_rollup", "Rollup single writer", "Two workers wrote the rollup table at once and doubled counts; the rollup job is now its single writer and workers only append events.")}},
		{name: "S6-34", q: "which command rotates the encryption key of stored credentials with vault-rekey and what guards it",
			want: "hfx_rekey", rows: []textRow{hfx("hfx_rekey", "vault-rekey re-encrypts stored credentials", "worker vault-rekey re-encrypts every stored credential under the newest key version; it refuses to run without --confirm and while another rekey holds the lock.")}},
	}
}

// TestShortExactRowShown: for each set-6 shape, the short row naming the
// asked thing is among ask's first three rows.
func TestShortExactRowShown(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	for _, c := range set6Cases() {
		rows := append(set6Background(at), c.rows...)
		o := build(parseQuestion(c.q, now), data{rows: rows}, 0, 0)
		ids := rowIDs(o.Rows)
		if i := indexOf(ids, c.want); i < 0 || i > 2 {
			t.Errorf("%s: rows %v, want %s in the first three:\n%s", c.name, ids, c.want, firstLines(o.Text(), 30))
		}
	}
}

func indexOf(ss []string, s string) int {
	for i, x := range ss {
		if x == s {
			return i
		}
	}
	return -1
}

func firstLines(s string, n int) string {
	ls := strings.Split(s, "\n")
	return strings.Join(ls[:min(n, len(ls))], "\n")
}

// TestLongRowCountsItsBestSection: a long row holding the question's
// words across different sections counts fully only those of its best
// section (and title); the others count a point each and are marked
// outside.
func TestLongRowCountsItsBestSection(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	spread := textRow{Table: "architecture_notes", ID: "anr_spread", Place: "kb/app", Title: "As built: spread", At: at, Body: longNote("As built: spread", 3000,
		[2]string{"Alpha", "The tool cli has one command per area."},
		[2]string{"Beta", "Each workflow run is kept for a month."},
	)}
	q := parseQuestion("which tool cli command starts a workflow run", now)
	s, ok := score(q, nil, &spread)
	if !ok {
		t.Fatal("the long row does not answer at all")
	}
	if len(s.outside) == 0 {
		t.Errorf("matched %v: no word is counted as held only outside the best section", s.matched)
	}
	whole := 3 * len(s.matched)
	if s.score >= whole {
		t.Errorf("score %d: the long row still counts every word its whole text holds (%d for %v)", s.score, whole, s.matched)
	}
}

// TestCommonHeadingWordAddsNothing: a long row's best section heading
// naming a word most scored rows hold ("commands") keeps no headWord; one
// naming a word few hold keeps it.
func TestCommonHeadingWordAddsNothing(t *testing.T) {
	t.Parallel()
	q := parseQuestion("which commands rekey the vault", now)
	var all []scored
	for i := range 9 {
		all = append(all, scored{row: &textRow{ID: fmt.Sprintf("mem_%d", i)}, score: 3, matched: []string{"commands"}})
	}
	all = append(all, scored{row: &textRow{ID: "anr_x"}, score: 3 + 3 + 2*headWord, matched: []string{"commands", "vault"}, heads: []string{"commands", "vault"}})
	distinctBonus(q, all)
	got := all[len(all)-1].score
	// commands: common, its heading point taken back; vault: rare, +1 and
	// its heading point kept
	if want := 3 + 3 + headWord + 1; got != want {
		t.Errorf("score %d, want %d: a common heading word still adds headWord, or a rare one lost it", got, want)
	}
}

// TestQuestionIdentifierAsWritten: a question writing an identifier
// (vault-rekey) ranks the row writing the same identifier as for a
// phrase.
func TestQuestionIdentifierAsWritten(t *testing.T) {
	t.Parallel()
	r := textRow{Table: "hotfixes", ID: "hfx_rekey", Body: "worker vault-rekey re-encrypts every stored credential; it refuses to run without --confirm."}
	q := parseQuestion("what guards does vault-rekey have", now)
	if got := identBonus(q, nil, &r); got < identStep {
		t.Errorf("identBonus %d for a row writing the question's identifier vault-rekey, want at least %d", got, identStep)
	}
}
