//go:build bench

package recall

// set5_resources_test.go — the resource-slot case of Sync benchmark set 5
// (devco D-968, bug hunt H-227): a product repo's folder holds a rule, a
// long as-built architecture note with ## sections, a hotfix, decisions
// and a memory; the org folder holds eight code_repo resources named
// after the product and its integration (shop, import, workflow,
// workspace, variant, pim) whose purpose repeats the questions' words. On
// 0.1.47 those resources took four to six of the answer's items and the
// row that answered was left out. Each question here must show its
// answering row in the top 3 rows, no code_repo line before a row, and at
// most one code_repo line.

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

// set5Resources maps each verbatim-style question to the title of the row
// that answers it (S5-15, S5-27 and an S5-24-shaped PIM question).
var set5Resources = map[string]string{
	"can two shop imports run at the same time for the same workflow and workspace": "One shop import per workflow and workspace",
	"how are variant options flagged for existing shop products in the catalog":     "is_choice_axis backfill",
	"which pim attribute does the shop import map a variant option to":              "PIM attribute for variant options",
}

// TestBenchSet5Resources writes the set-5 resource shape and asks each
// question of set5Resources.
func TestBenchSet5Resources(t *testing.T) {
	env, app, bin := prepareSet5Resources(t)
	var misses []string
	for q, want := range set5Resources {
		out, _ := linkedRun(t, env, app, bin, "ask", "--query", q, "--json")
		var a struct {
			Rows []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"rows"`
			Ranked []struct {
				Kind string `json:"kind"`
				ID   string `json:"id"`
			} `json:"ranked"`
		}
		if err := json.Unmarshal([]byte(out), &a); err != nil {
			t.Fatalf("%q: %v\n%s", q, err, out)
		}
		titles := map[string]string{}
		for _, r := range a.Rows {
			titles[r.ID] = r.Title
		}
		at, rows, res, resBeforeRow := -1, 0, 0, false
		for _, r := range a.Ranked {
			switch r.Kind {
			case "row":
				if titles[r.ID] == want && rows < 3 {
					at = rows
				}
				rows++
				resBeforeRow = resBeforeRow || res > 0
			case "resource":
				res++
			}
		}
		if at < 0 || res > 1 || resBeforeRow {
			text, _ := linkedRun(t, env, app, bin, "ask", "--query", q)
			misses = append(misses, fmt.Sprintf("%q: answering row %q at %d (want top 3), %d code_repo items (want <= 1), a resource before a row: %v\n%s", q, want, at, res, resBeforeRow, text))
		}
	}
	if len(misses) > 0 {
		t.Errorf("lore ask on the set-5 resource shape missed %d case(s):\n  %s", len(misses), strings.Join(misses, "\n  "))
	}
}

// prepareSet5Resources writes the shape into a temp HOME with one local
// bare store and returns the environment, the repo dir and the binary.
func prepareSet5Resources(t *testing.T) ([]string, string, string) {
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
	for _, f := range []string{"purpose", "url"} {
		lore("resource", "field", "add", "--kind", "code_repo", "--field", f, "--type", "text", "--to", "kb/org", "--yes")
	}
	for _, n := range []string{"shop_app", "shop_import_worker", "sync_shop", "pim_shop_bridge", "workflow_engine", "workspace_api", "shop_variant_mapper", "import_scheduler"} {
		lore("resource", "add", "--kind", "code_repo", "--name", n,
			"--set", "purpose=Shop and PIM integration: imports products, variants, options and attributes from a shop workspace into the catalog, per workflow",
			"--set", "url=https://git.example.test/"+n, "--to", "kb/org", "--yes")
	}
	var note strings.Builder
	note.WriteString("As built: the catalog import reads a shop workspace in pages of 250 products, maps variants and options to catalog rows and writes them through the one writer.")
	for _, s := range []string{"Fetching", "Mapping", "Writing", "Progress", "Retries", "Webhooks", "Media", "Prices", "Locales", "Cleanup"} {
		fmt.Fprintf(&note, "\n\n## %s\n\nThe %s step of the catalog import runs inside the import workflow for one workspace. It reads the run row, logs each page it handles with the run id, records counters for products, variants and options, and hands the page to the next step through the job queue.", s, s)
	}
	lore("architecturenote", "add", "--to", "kb/app", "--title", "As built: catalog import", "--body", note.String(), "--yes")
	lore("rule", "add", "--to", "kb/app", "--body", "Every shop import records who started it (workflow and user) on its run.", "--yes")
	lore("hotfix", "add", "--to", "kb/app", "--title", "One shop import per workflow and workspace", "--body", "Only one shop import may run per workflow and workspace at a time: a second start returns ErrImportRunning (the lock key is workflow id + workspace id).", "--yes")
	lore("decision", "add", "--to", "kb/app", "--title", "is_choice_axis backfill", "--body", "Backfill is_choice_axis for existing products: options of a shop product with more than one variant are variant options; the backfill runs once per workspace in batches of 1000.", "--yes")
	lore("decision", "add", "--to", "kb/app", "--title", "PIM attribute for variant options", "--body", "A shop variant option maps to the PIM attribute option_value of the variant's product; the import never writes a variant option into a free-text attribute.", "--yes")
	lore("memory", "add", "--to", "kb/app", "--body", "The shop workspace token is read from the vault on every import start.", "--yes")
	return env, app, bin
}
