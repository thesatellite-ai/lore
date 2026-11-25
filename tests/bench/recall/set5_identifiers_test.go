//go:build bench

package recall

// set5_identifiers_test.go — the recall gate's set-5 identifier case (bug
// hunt H-230, devco D-971; Sync benchmark set 5 S5-06 and S5-11): on a
// store shaped like the benchmark's (an org folder of code_repo cards, a
// long as-built architecture note with ## sections, many short notes and
// a decision sharing the question's common words, an off-topic hotfix),
// the short memories naming the asked errors as code identifiers
// (ErrGroupNameTaken, ErrBuiltinGroupLocked) are in the top three rows, and
// the hotfix naming JobTimeLimit leads the answer about the job time
// limit. Before the fix, ask read ErrGroupNameTaken as one word that
// holds none of "taken group name", and the rows sharing only "group",
// "name", "sync workflow run" won.

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

// set5IdentCase is one set-5 question: the text each answering row holds
// and how many of the first rows must hold them all.
type set5IdentCase struct {
	id, question string
	answers      []string
	within       int
}

// set5IdentCases are S5-06 (top three) and S5-11 (first).
var set5IdentCases = []set5IdentCase{
	{"S5-06", "what happens when a custom group uses a taken group name, and can someone edit a locked builtin group", []string{"ErrGroupNameTaken", "ErrBuiltinGroupLocked"}, 3},
	{"S5-11", "what is the job time limit of a sync workflow run and why was it raised", []string{"JobTimeLimit raised"}, 1},
}

// TestBenchSet5Identifiers writes the set-5 identifier shape and asks
// each set5IdentCases question.
func TestBenchSet5Identifiers(t *testing.T) {
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
			"set": []string{fmt.Sprintf("purpose=The gizmo service %d: workspace groups, sync workflow runs, the workflow client and admin screens", i)}})
	}
	// a long as-built note sharing the common words, holding no answer
	var arch strings.Builder
	for s, head := range []string{"Workspaces", "Groups", "Sync workflows", "Workflow client", "Admin screens", "Audit"} {
		fmt.Fprintf(&arch, "## %s\n\n", head)
		for p := range 6 {
			fmt.Fprintf(&arch, "Section %d part %d: each workspace keeps its groups and names in the group table; the sync workflow run is started by the workflow client, which records every run and its error for the admin screens. ", s, p)
		}
		arch.WriteString("\n\n")
	}
	line("architecturenote add", map[string]any{"to": "kb/app", "title": "As built: workspaces, groups and sync workflows", "body": arch.String()})
	for i := 1; i <= 60; i++ {
		body := fmt.Sprintf("Note %d: the workspace group screen lists each group, its name and the builtin flag; the sync workflow run page shows the client error for each run.", i)
		if i%3 == 0 {
			line("decision add", map[string]any{"to": "kb/app", "title": fmt.Sprintf("Decision %d: group screens and workflow runs", i), "body": body})
			continue
		}
		line("memory add", map[string]any{"to": "kb/app", "body": body})
	}
	line("decision add", map[string]any{"to": "kb/app", "title": "Workspace groups: builtin groups plus custom groups", "body": "Each workspace gets the builtin groups owner, admin and member when it is created; a custom group has its own name, unique in the workspace, and builtin groups are never deleted. We chose this because every workspace needs the same base roles."})
	line("memory add", map[string]any{"to": "kb/app", "body": "group.Create returns ErrGroupNameTaken (409) when the new name collides with a seeded name (owner, admin, member)."})
	line("memory add", map[string]any{"to": "kb/app", "body": "group.Update and group.Delete return ErrBuiltinGroupLocked for a seeded group; only custom groups change."})
	line("hotfix add", map[string]any{"to": "kb/app", "title": "Workflow client error map", "body": "The sync workflow client maps each run error of the engine (cancelled, failed, timed out) to a typed sync error; a run that timed out was shown as an internal error until the map listed it."})
	line("hotfix add", map[string]any{"to": "kb/app", "title": "JobTimeLimit raised to 1 h", "body": "Long catalog imports hit the 30 min JobTimeLimit and stopped mid-batch; JobTimeLimit is now 1 h in the workflow options."})
	batch := filepath.Join(dir, "rows.jsonl")
	if err := os.WriteFile(batch, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	lore("batch", "apply", "--file", batch, "--yes")
	var misses []string
	for _, c := range set5IdentCases {
		var out struct {
			Rows []struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"rows"`
		}
		raw := lore("ask", "--query", c.question, "--json")
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("%s: ask --json: %v\n%s", c.id, err, raw)
		}
		first := out.Rows[:min(c.within, len(out.Rows))]
		for _, want := range c.answers {
			found := false
			for _, r := range first {
				if strings.Contains(r.Title+" "+r.Body, want) {
					found = true
				}
			}
			if !found {
				misses = append(misses, fmt.Sprintf("%s %q: no row holding %q in the first %d rows:\n%s", c.id, c.question, want, c.within, lore("ask", "--query", c.question)))
			}
		}
		t.Logf("%s: first %d rows checked", c.id, c.within)
	}
	if len(misses) > 0 {
		t.Errorf("set-5 identifier case missed %d answer(s):\n%s", len(misses), strings.Join(misses, "\n"))
	}
}
