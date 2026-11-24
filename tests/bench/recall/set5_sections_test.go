//go:build bench

package recall

// set5_sections_test.go — the long-note sections case of Sync benchmark
// set 5 (devco D-969, bug hunt H-228, asks 2 and 4): a project folder
// whose as-built architecture notes are several KB with ## sections, short
// decisions and memories sharing more of the question's words, twenty
// code_repo resources in the org folder whose purpose repeats them, and an
// off-topic hotfix. The note whose section answers must rank first (the
// owner: "must beat" the short rows) and print that section first, marked
// "(matching section)"; a note whose sections all repeat the question's
// words must leave room for the memory that answers (S5-04).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// set5SecFiller is a long note's other sections: no word of the questions.
var set5SecFiller = []string{
	"The parcel desk prints zebra labels at the dock and carriers collect parcels twice daily.",
	"Invoices go out on the first weekday; the ledger closes when finance signs the monthly sheet.",
	"Theme colours come from the brand palette; the admin screens use the compact density.",
	"Image thumbnails are resized by the media worker into three widths and stored in buckets.",
	"Search synonyms are curated by merchandising and pushed to the index each night.",
	"Translations live in locale files and the fallback locale is English for every storefront.",
}

// set5SecNote is a markdown note of about size characters: its title, the
// given sections, then filler sections.
func set5SecNote(title string, size int, secs ...[2]string) string {
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	for _, s := range secs {
		b.WriteString("## " + s[0] + "\n\n" + s[1] + "\n\n")
	}
	for n := 0; b.Len() < size; n++ {
		fmt.Fprintf(&b, "## Notes %d\n\n%s %s %s\n\n", n+1, set5SecFiller[n%len(set5SecFiller)], set5SecFiller[(n+2)%len(set5SecFiller)], set5SecFiller[(n+4)%len(set5SecFiller)])
	}
	return b.String()
}

// set5SectionCase is one question: the text that must lead the answer
// (top 1) and the fact the printed section must hold.
type set5SectionCase struct{ q, lead, fact string }

var set5SectionCases = []set5SectionCase{
	{"what stops a reviewer deciding on their own change and what is remaining", "As built: approvals service", "ErrOwnApproval"},
	{"in which order does the container entrypoint run its boot steps", "As built: runtime", "migrations, then the cache warm"},
	{"which fields does a catalog row carry and which step fills the price fields", "As built: catalog sync", "price_cents"},
}

// TestBenchSet5Sections writes the shape and asks each question.
func TestBenchSet5Sections(t *testing.T) {
	dir := t.TempDir()
	if d := os.Getenv(envDir); d != "" {
		dir = filepath.Join(d, "set5-sections")
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
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
	home, app := filepath.Join(dir, "home"), filepath.Join(dir, "app")
	for _, d := range []string{home, app} {
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
	linkedRun(t, env, app, git, "init", "-q")
	lore := func(args ...string) string { out, _ := linkedRun(t, env, app, bin, args...); return out }
	lore("init", "--yes")
	bare := filepath.Join(dir, "srv", "kb.git")
	linkedRun(t, env, dir, git, "init", "-q", "--bare", "-b", "main", bare)
	lore("store", "init", "--name", "kb", "--url", bare, "--yes")
	for _, f := range []string{"kb/org", "kb/app"} {
		lore("memory", "add", "--to", f, "--create", "--body", "Folder "+f+" holds the notes of that area.", "--yes")
	}
	lore("resource", "kind", "add", "--kind", "code_repo", "--to", "kb/org", "--yes")
	lore("resource", "field", "add", "--kind", "code_repo", "--field", "purpose", "--type", "text", "--to", "kb/org", "--yes")
	for i := range 20 {
		lore("resource", "add", "--kind", "code_repo", "--name", fmt.Sprintf("shop_part%02d", i), "--set", "purpose=Part of the shop: catalog rows and fields, reviewer screens, container boot steps, the entrypoint and job runner", "--to", "kb/org", "--yes")
	}
	note := func(title, body string) {
		lore("architecturenote", "add", "--to", "kb/app", "--title", title, "--body", body, "--meta", "fact_state=live", "--meta", "fact_verified_at=2026-11-20", "--yes")
	}
	note("As built: approvals service", set5SecNote("As built: approvals service", 5200,
		[2]string{"Overview", "The approvals service holds approval requests for catalog edits and price edits."},
		[2]string{"Reviewer self decision", "APPROVALS_ON turns the gate on. A reviewer deciding on their own change gets ErrOwnApproval. Remaining: bulk decisions and an audit export."}))
	note("As built: module map", set5SecNote("As built: module map", 2600,
		[2]string{"Modules", "The container runs the web module, the worker module and the scheduler; each step of a job runs in the worker."},
		[2]string{"Order of modules", "Modules start in order: config, store, web; the entrypoint of each is its main package."}))
	note("As built: runtime", set5SecNote("As built: runtime", 5600,
		[2]string{"Process model", "One process per container; signals go to the supervisor."},
		[2]string{"Entrypoint boot order and START_TOKEN", "The entrypoint runs migrations, then the cache warm, then the HTTP listener. START_TOKEN is a random id minted at boot and stamped on every log line of that boot."}))
	note("As built: catalog sync", set5SecNote("As built: catalog sync", 6000,
		[2]string{"Feed fetch", "The supplier feed is fetched hourly into object storage."},
		[2]string{"Catalog row fields", "A catalog row carries sku, title, price_cents, compare_at_cents, stock_qty and variant_options; the mapper step fills the price fields from the supplier price list."}))
	var job strings.Builder
	job.WriteString("# As built: job runner\n\n")
	for i := 0; job.Len() < 3800; i++ {
		fmt.Fprintf(&job, "## Job runner part %d\n\nIn this part a job is cancelled, abandoned or retried by the cli only through the runner. %s %s\n\n", i+1, set5SecFiller[i%len(set5SecFiller)], set5SecFiller[(i+3)%len(set5SecFiller)])
	}
	note("As built: job runner", job.String())
	lore("memory", "add", "--to", "kb/app", "--body", "A job is cancelled by cancel, abandoned after three missed beats and retried by retry; the cli acts as the cli actor.", "--yes")
	lore("decision", "add", "--to", "kb/app", "--title", "Reviewers own their queue", "--body", "A reviewer stops a change that breaks the build; their own tests run first.", "--yes")
	lore("memory", "add", "--to", "kb/app", "--body", "The remaining reviewer work: deciding who stops a stale change on their own.", "--yes")
	lore("decision", "add", "--to", "kb/app", "--title", "Change review is remaining work", "--body", "Deciding the review rota is remaining; a reviewer can stop a change.", "--yes")
	lore("memory", "add", "--to", "kb/app", "--body", "The container entrypoint runs the boot steps in order and logs each step.", "--yes")
	lore("decision", "add", "--to", "kb/app", "--title", "Catalog rows stay flat", "--body", "Catalog rows carry flat fields; the step that fills a row never nests fields, and price fields stay integers.", "--yes")
	lore("memory", "add", "--to", "kb/app", "--body", "Which step fills the catalog fields: the import step fills every row field.", "--yes")
	lore("hotfix", "add", "--to", "kb/app", "--title", "Client error map", "--body", "The payment client maps a 429 to a retry after the header's delay.", "--yes")
	var misses []string
	for _, c := range set5SectionCases {
		out := lore("ask", "--query", c.q)
		first := strings.Index(out, "\n- ")
		at, mark := strings.Index(out, c.lead), strings.Index(out, "(matching section)")
		if at < 0 || first >= 0 && strings.Index(out[first+1:], "\n- ") >= 0 && at > first+1+strings.Index(out[first+1:], "\n- ") {
			misses = append(misses, fmt.Sprintf("%q: %q does not lead the answer:\n%s", c.q, c.lead, out))
			continue
		}
		if mark < at || !strings.Contains(out[mark:], c.fact) {
			misses = append(misses, fmt.Sprintf("%q: the matching section (%q) is not printed first, marked:\n%s", c.q, c.fact, out))
		}
	}
	// S5-04: the long note leaves room for the memory that answers
	if out := lore("ask", "--query", "how is a job cancelled, abandoned or retried by the cli"); !strings.Contains(out, "cli actor") {
		misses = append(misses, fmt.Sprintf("S5-04: the memory that answers is not printed next to the long note:\n%s", out))
	}
	if len(misses) > 0 {
		t.Errorf("set 5 long-note sections missed %d case(s):\n%s", len(misses), strings.Join(misses, "\n\n"))
	}
}
