//go:build bench

package recall

// set6_cards_test.go — the resource-card case of Sync benchmark set 6
// (devco D-975, bug hunt H-234, S6-19): the org folder holds code_repo
// resources a question word is a typo of (webhooks, receivers), a part of
// (paygate_bridge) or whose field value it says, with long purposes as
// real repo cards have; the product folder holds the note, the memory and
// the memory-shaped task that answer. On 0.1.48 "why is the payment
// webhook receiver not mounted" printed the note and two full cards and
// left out the memory and the task. Each question here must show its
// answering rows before any code_repo line, with at most one code_repo
// line; the sweep counts the cards over every question: a card only where
// the question names the resource by its name.

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

// set6Cards maps each question to the bodies (a distinctive part) of the
// rows that must be shown before any code_repo line.
var set6Cards = map[string][]string{
	"why is the Paygate webhook receiver not mounted":         {"Dockerfile.hookrcv", "behind the gateway"},
	"how does the paygate integration retry a failed payment": {"retries a failed payment"},
	"which events does the receiver drop":                     {"drops ping events"},
}

// set6Named is the question that names a resource by its name: its card
// is the one card the sweep allows.
const set6Named = "what does hookrcv_ops deploy"

// TestBenchSet6CardsAlone writes the set-6 card shape and asks each
// question of set6Cards, then sweeps the card count.
func TestBenchSet6CardsAlone(t *testing.T) {
	env, app, bin := prepareSet6Cards(t)
	var misses []string
	cards := map[string]int{}
	for q, wants := range set6Cards {
		out, _ := linkedRun(t, env, app, bin, "ask", "--query", q)
		card := strings.Index(out, "\ncode_repo ")
		for _, w := range wants {
			if at := strings.Index(out, w); at < 0 || card >= 0 && card < at {
				misses = append(misses, fmt.Sprintf("%q: %q not shown before the code_repo lines:\n%s", q, w, out))
			}
		}
		cards[q] = strings.Count(out, "\ncode_repo ")
		if cards[q] > 1 {
			misses = append(misses, fmt.Sprintf("%q: %d code_repo lines (want <= 1):\n%s", q, cards[q], out))
		}
	}
	// the named resource keeps its card
	out, _ := linkedRun(t, env, app, bin, "ask", "--query", set6Named, "--json")
	var a struct {
		Resources []struct {
			Name string `json:"name"`
		} `json:"resources"`
	}
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatalf("%q: %v\n%s", set6Named, err, out)
	}
	if len(a.Resources) == 0 || a.Resources[0].Name != "hookrcv_ops" {
		misses = append(misses, fmt.Sprintf("%q: the resource it names is not in the answer:\n%s", set6Named, out))
	}
	total := 0
	for _, n := range cards {
		total += n
	}
	t.Logf("set-6 card sweep: %d code_repo lines over %d questions that name no resource (%v)", total, len(cards), cards)
	if len(misses) > 0 {
		t.Errorf("lore ask on the set-6 card shape missed %d case(s):\n  %s", len(misses), strings.Join(misses, "\n  "))
	}
}

// prepareSet6Cards writes the shape into a temp HOME with one local bare
// store and returns the environment, the repo dir and the binary.
func prepareSet6Cards(t *testing.T) ([]string, string, string) {
	t.Helper()
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
	if err := os.WriteFile(filepath.Join(app, "README.md"), []byte("app\n"), 0o600); err != nil {
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
	linkedRun(t, env, app, git, "init", "-q")
	linkedRun(t, env, app, git, "add", "-A")
	linkedRun(t, env, app, git, "commit", "-qm", "init")
	lore("init", "--yes")
	bare := filepath.Join(srv, "kb.git")
	linkedRun(t, env, srv, git, "init", "-q", "--bare", "-b", "main", bare)
	lore("store", "init", "--name", "kb", "--url", bare, "--yes")
	lore("store", "folder", "add", "--store", "kb", "--folder", "app")
	lore("resource", "kind", "add", "--kind", "code_repo", "--description", "A git repository of a product", "--to", "kb/org", "--yes")
	for _, f := range []string{"purpose", "provider", "url"} {
		lore("resource", "field", "add", "--kind", "code_repo", "--field", f, "--type", "text", "--to", "kb/org", "--yes")
	}
	long := strings.Repeat("records every event with its signature, retries and run id; ", 28)
	for _, n := range []string{"webhooks", "receivers", "paygate_bridge", "billing_core", "hookrcv_ops"} {
		lore("resource", "add", "--kind", "code_repo", "--name", n,
			"--set", "purpose=Payment events service: "+long, "--set", "provider=paygate",
			"--set", "url=https://git.example.test/"+n, "--to", "kb/org", "--yes")
	}
	lore("architecturenote", "add", "--to", "kb/app", "--title", "As built: payment webhooks", "--body", "Payment webhooks from Paygate arrive at the separate hookrcv service, not the API server; it verifies the signature, drops ping events and enqueues a job.", "--yes")
	lore("memory", "add", "--to", "kb/app", "--body", "The receiver is not mounted in the API on purpose: it runs as its own binary built from Dockerfile.hookrcv.", "--yes")
	lore("memory", "add", "--to", "kb/app", "--body", "Mount the Paygate receiver behind the gateway once the API splits.", "--yes")
	lore("memory", "add", "--to", "kb/app", "--body", "A payment that fails is retried by the job queue: the worker retries a failed payment three times with backoff, then marks it failed.", "--yes")
	lore("memory", "add", "--to", "kb/app", "--body", "Release order: receivers first, then webhooks; hookrcv_ops deploys both.", "--yes")
	return env, app, bin
}
