//go:build bench

package recall

// linked_test.go — the linked-stores shape of the recall gate (bug hunt
// H-209, devco:D-950): a team's code repo with four stores attached (an
// org folder of code_repo resources with closed-set fields, three project
// folders in two more stores, a personal store), ~550 rows with metadata
// on every row (fact_evidence naming two code files each, fact_verified_at,
// fact_topic) and 300 comments, over a code history of 300 commits. lore
// ask took ~15 s per call on a store of this shape, one git log per
// evidence file of every candidate row; the bars keep it fast: the cold
// call (the first after a store write) under 2 s, the warm call (the
// median of three) under linkedWarmBar.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The linked shape's bars.
const (
	linkedColdBar = 2 * time.Second
	// linkedWarmBar is above the personal store's 50 ms: a call here reads
	// four stores and checks every candidate row's evidence in the code
	// repo (one git log); measured ~190 ms warm on the dev box at D-950.
	linkedWarmBar = 400 * time.Millisecond
)

// linkedQuestions are asked of the linked shape: rows answer each.
var linkedQuestions = []string{
	"why does the billing webhook queue retry per batch",
	"who owns the invoice export runbook",
	"how does the import step hand off to search",
}

// linkedRun runs lore (or git) in dir with the bench's environment.
func linkedRun(t *testing.T, env []string, dir, bin string, args ...string) (string, time.Duration) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir, cmd.Env = dir, env
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(bin), strings.Join(args, " "), err, out)
	}
	return string(out), time.Since(start)
}

// TestBenchLinkedStoresAsk writes the linked shape and times lore ask on it.
func TestBenchLinkedStoresAsk(t *testing.T) {
	p := prepareLinked(t)
	var misses []string
	for i, q := range linkedQuestions {
		// cold: the first call after a write, as after a sync
		linkedRun(t, p.env, p.app, p.bin, "memory", "add", "--to", "kb/app", "--body", fmt.Sprintf("Heartbeat %d of the bench.", i), "--yes")
		out, cold := linkedRun(t, p.env, p.app, p.bin, "ask", "--query", q)
		if !strings.Contains(out, "retries per batch") {
			t.Fatalf("ask %q printed no project row:\n%s", q, out)
		}
		var warm []time.Duration
		for range 3 {
			_, d := linkedRun(t, p.env, p.app, p.bin, "ask", "--query", q)
			warm = append(warm, d)
		}
		slices.Sort(warm)
		t.Logf("linked shape: %q cold %d ms, warm %d/%d/%d ms", q, cold.Milliseconds(), warm[0].Milliseconds(), warm[1].Milliseconds(), warm[2].Milliseconds())
		if cold > linkedColdBar {
			misses = append(misses, fmt.Sprintf("%q: cold %d ms, bar %d ms", q, cold.Milliseconds(), linkedColdBar.Milliseconds()))
		}
		if warm[1] > linkedWarmBar {
			misses = append(misses, fmt.Sprintf("%q: warm %d ms, bar %d ms", q, warm[1].Milliseconds(), linkedWarmBar.Milliseconds()))
		}
	}
	if len(misses) > 0 {
		t.Errorf("lore ask on the linked-stores shape missed %d bar(s):\n  %s", len(misses), strings.Join(misses, "\n  "))
	}
}

// linkedShape is the written shape: the code repo and the environment.
type linkedShape struct {
	app, bin string
	env      []string
}

// prepareLinked writes the shape under a temp folder (LORE_BENCH_DIR/linked
// when set) with the bench's binary (LORE_BENCH_BIN, else a build).
func prepareLinked(t *testing.T) linkedShape {
	t.Helper()
	dir := t.TempDir()
	if d := os.Getenv(envDir); d != "" {
		dir = filepath.Join(d, "linked")
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
	lore := func(args ...string) { linkedRun(t, env, app, bin, args...) }
	// the code history: 300 commits over 40 packages of 5 files
	linkedRun(t, env, app, git, "init", "-q")
	for c := 1; c <= 300; c++ {
		for k := 1; k <= 3; k++ {
			f := filepath.Join(app, "internal", fmt.Sprintf("p%d", (c*k*7)%40), fmt.Sprintf("f%d.go", (c+k)%5))
			if err := os.MkdirAll(filepath.Dir(f), 0o750); err != nil {
				t.Fatal(err)
			}
			fh, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(fh, "// change %d %d\n", c, k)
			fh.Close()
		}
		linkedRun(t, env, app, git, "add", "-A")
		linkedRun(t, env, app, git, "commit", "-qm", fmt.Sprintf("change %d", c))
	}
	lore("init", "--yes")
	for _, s := range []string{"kb", "ops", "docs"} {
		bare := filepath.Join(srv, s+".git")
		linkedRun(t, env, srv, git, "init", "-q", "--bare", "-b", "main", bare)
		lore("store", "init", "--name", s, "--url", bare, "--yes")
	}
	lore("store", "init", "--name", "me", "--yes")
	lore("resource", "kind", "add", "--kind", "code_repo", "--description", "A git repository of a product", "--to", "kb/org", "--yes")
	lore("resource", "field", "add", "--kind", "code_repo", "--field", "project", "--type", "text", "--required", "--values", "gizmo,widget,tools", "--to", "kb/org", "--yes")
	lore("resource", "field", "add", "--kind", "code_repo", "--field", "purpose", "--type", "text", "--to", "kb/org", "--yes")
	lore("resource", "field", "add", "--kind", "code_repo", "--field", "status", "--type", "text", "--values", "active,dormant,archived", "--to", "kb/org", "--yes")
	folders := []string{"kb/app", "ops/infra", "ops/billing", "docs/guide"}
	for _, f := range folders {
		lore("memory", "add", "--to", f, "--create", "--body", "Folder "+f+" holds the notes of that area.", "--yes")
	}
	words := []string{"import", "export", "billing", "invoice", "webhook", "queue", "retry", "cache", "admin", "login", "token", "session", "report", "schedule", "migration", "index", "search", "feed", "sync", "worker"}
	projects, states := []string{"widget", "gizmo", "tools"}, []string{"active", "dormant", "archived"}
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		p := projects[i%3]
		fmt.Fprintf(&b, `{"op":"resource add","input":{"kind":"code_repo","name":"%s_repo_%02d","set":["project=%s","purpose=The %s service %d: API, import workers, public URL and admin","status=%s"],"to":"kb/org"}}`+"\n", p, i, p, p, i, states[i%3])
	}
	for i := 1; i <= 500; i++ {
		f, a, w2, w3 := folders[i%4], words[i%20], words[(i*7)%20], words[(i*13)%20]
		body := fmt.Sprintf("Note %d on the %s path: the %s step hands off to %s with retries per batch; owner checks the %s dashboard weekly and the %s runbook lists every flag.", i, a, w2, w3, a, w2)
		meta := fmt.Sprintf(`"area=%s","fact_state=verified","fact_evidence=internal/p%d/f%d.go:12,internal/p%d/f%d.go:40","fact_verified_at=2026-11-01","fact_topic=%s"`, a, i%40, i%5, (i*3)%40, (i+1)%5, a)
		switch i % 4 {
		case 0:
			fmt.Fprintf(&b, `{"op":"memory add","input":{"to":"%s","body":"%s","meta":[%s]}}`+"\n", f, body, meta)
		case 1:
			fmt.Fprintf(&b, `{"op":"decision add","input":{"to":"%s","title":"Decision %d: %s goes through %s","body":"%s","meta":[%s]}}`+"\n", f, i, a, w2, body, meta)
		case 2:
			fmt.Fprintf(&b, `{"op":"task add","input":{"to":"%s","commitment":"accepted","title":"Task %d: wire %s into %s","body":"%s","meta":[%s]}}`+"\n", f, i, a, w2, body, meta)
		default:
			fmt.Fprintf(&b, `{"op":"rule add","input":{"to":"%s","body":"Rule %d: %s never skips %s. %s","meta":[%s]}}`+"\n", f, i, a, w2, body, meta)
		}
	}
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&b, `{"op":"memory add","input":{"to":"me","body":"Personal note %d about %s and %s."}}`+"\n", i, words[i%20], words[(i*3)%20])
	}
	batch := filepath.Join(dir, "rows.jsonl")
	if err := os.WriteFile(batch, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	lore("batch", "apply", "--file", batch, "--yes")
	// 300 comments: two on each of 150 rows
	out, _ := linkedRun(t, env, app, bin, "memory", "list", "--json")
	ids := idsIn(out, "mem_")
	for _, noun := range []string{"decision", "task"} {
		o, _ := linkedRun(t, env, app, bin, noun, "list", "--json")
		ids = append(ids, idsIn(o, "dec_", "tsk_", "task_")...)
	}
	b.Reset()
	for n, id := range ids[:min(150, len(ids))] {
		fmt.Fprintf(&b, `{"op":"comment add","input":{"on_id":"%s","body":"Comment %d: checked the import and webhook path again; retries per batch still hold."}}`+"\n", id, n)
		fmt.Fprintf(&b, `{"op":"comment add","input":{"on_id":"%s","body":"Follow-up %d: admin and billing owners agreed."}}`+"\n", id, n)
	}
	if err := os.WriteFile(batch, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	lore("batch", "apply", "--file", batch, "--yes")
	_, first := linkedRun(t, env, app, bin, "ask", "--query", linkedQuestions[0])
	t.Logf("linked shape written in %s; first read (every cache built) %d ms", dir, first.Milliseconds())
	return linkedShape{app: app, bin: bin, env: env}
}

// idsIn is every "id" value in a list's --json output with one of prefixes.
func idsIn(out string, prefixes ...string) []string {
	var ids []string
	for _, part := range strings.Split(out, `"id": "`)[1:] {
		id, _, _ := strings.Cut(part, `"`)
		for _, p := range prefixes {
			if strings.HasPrefix(id, p) {
				ids = append(ids, id)
				break
			}
		}
	}
	return ids
}
