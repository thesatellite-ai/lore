//go:build userscen

package userscen

// Shared fixtures of the tasks campaign (TestCampaignTasks_*): the whole
// command list of the tasks area (task, tasklist, mission, plan, workflow,
// reminder, gate, hold, run, session, inbox: 104 commands), the id
// probes every lookup takes, and small readers of the --json documents.
// It builds on the tasks area's fixtures (tasks_helpers_test.go:
// newTasksSolo, newTasksTeam, tasksAdd, tasksShow, tasksJSONErr) and the
// REST harness (rest_harness_test.go: restStart). Every name starts with
// campTasks so no other file in this package collides with it.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// campTasksCommands is every command of the tasks area, as typed after
// `lore` (the campaign inventory, 104 commands).
var campTasksCommands = []string{
	"gate approve",
	"gate request",
	"hold claim",
	"hold release",
	"inbox show",
	"mission add",
	"mission apply",
	"mission delete",
	"mission done",
	"mission edit",
	"mission list",
	"mission pause",
	"mission resume",
	"mission search",
	"mission show",
	"mission status",
	"plan add",
	"plan delete",
	"plan edit",
	"plan list",
	"plan search",
	"plan show",
	"reminder add",
	"reminder delete",
	"reminder done",
	"reminder list",
	"run cancel",
	"run end",
	"run start",
	"session list",
	"task accept",
	"task add",
	"task board",
	"task cancel",
	"task checklist add",
	"task checklist edit",
	"task checklist import",
	"task checklist list",
	"task checklist promote",
	"task checklist rm",
	"task checklist tick",
	"task checklist untick",
	"task claim",
	"task decline",
	"task deferred",
	"task delete",
	"task done",
	"task edit",
	"task export",
	"task heartbeat",
	"task history",
	"task import",
	"task list",
	"task merged",
	"task next",
	"task note",
	"task reassign",
	"task recur edit",
	"task recur show",
	"task recur skip",
	"task recur stop",
	"task relate",
	"task release",
	"task revoke",
	"task search",
	"task show",
	"task someday",
	"task split",
	"task stale",
	"task start",
	"task template add",
	"task template delete",
	"task template edit",
	"task template list",
	"task template rename",
	"task template show",
	"task time edit",
	"task time log",
	"task time restore",
	"task time rm",
	"task time show",
	"task time stop",
	"task tree check",
	"task triage",
	"task unrelate",
	"task unwatch",
	"task view add",
	"task view delete",
	"task view edit",
	"task view list",
	"task view show",
	"task watch",
	"tasklist add",
	"tasklist delete",
	"tasklist edit",
	"tasklist list",
	"tasklist search",
	"tasklist show",
	"workflow add",
	"workflow delete",
	"workflow edit",
	"workflow list",
	"workflow search",
	"workflow show",
}

// campTasksUnknownHex is a well-formed id body (32 hex, UUIDv7 shape) no
// row has: with a kind's prefix it is "a real id of a row that is gone".
const campTasksUnknownHex = "01a1b7b8ab977a378fb61305d8eceb5c"

// campTasksLong is a 10k-character value (the "very long" odd value).
var campTasksLong = strings.Repeat("long title ", 1000)[:10000]

// campTasksOdd is the matrix of odd values every title/name-taking add is
// run with: each must round-trip unchanged through add → show.
var campTasksOdd = []struct{ name, value string }{
	{"unicode", "Déjà vu — naïve café"},
	{"emoji", "Ship it 🚀🔥"},
	{"rtl", "مرحبا بالعالم"},
	{"cjk", "修复登录重定向"},
	{"reserved words", "null"},
	{"sql-ish", "'; DROP TABLE tasks; --"},
	{"spaces and slashes", "  a/b\\c  with   spaces "},
	{"markdown", "Fix [ ] the # heading - *now*"},
}

// campTasksErr decodes the --json error document of a failed run (stdout).
func campTasksErr(t *testing.T, r th.Result) tasksErrEnvelope {
	t.Helper()
	var e tasksErrEnvelope
	if err := json.Unmarshal([]byte(r.Stdout), &e); err != nil || e.Error.Code == "" {
		t.Fatalf("no --json error document (%v): %s", err, r)
	}
	if r.Code == 0 || e.Error.Exit != r.Code {
		t.Fatalf("error document exit %d, process exit %d: %s", e.Error.Exit, r.Code, r)
	}
	return e
}

// campTasksData decodes the data member of a --json success document into
// a generic map (the field-shape checks read keys and JSON types).
func campTasksData(t *testing.T, r th.Result) map[string]json.RawMessage {
	t.Helper()
	r.OK(t)
	var env struct {
		SchemaVersion int                        `json:"schema_version"`
		Kind          string                     `json:"kind"`
		Data          map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil || env.Kind == "" {
		t.Fatalf("no {schema_version, kind, data} document (%v): %s", err, r)
	}
	return env.Data
}

// campTasksStr reads a string field of a decoded data member ("" when the
// field is missing or not a string).
func campTasksStr(m map[string]json.RawMessage, k string) string {
	var s string
	_ = json.Unmarshal(m[k], &s)
	return s
}

// campTasksTreeHash lists every file under dir with its hash, except .git internals the
// read path may touch (index stat data, FETCH_HEAD) and the local cache:
// two equal hashes mean a command wrote nothing a person would see (rows,
// refs, committed files), and campTasksTreeDiff names what differs.
func campTasksTreeHash(t *testing.T, dir string) string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			if rel == filepath.Join(".git", "objects") || rel == filepath.Join(".git", "logs") {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Base(p) {
		case "index", "FETCH_HEAD", "ORIG_HEAD":
			if strings.HasPrefix(rel, ".git") {
				return nil
			}
		}
		// The local cache (.lore/lore-cache.db, git-ignored, rebuilt from the
		// rows) may be refreshed by any read; it is not a row a person sees.
		if strings.HasPrefix(filepath.Base(p), "lore-cache.db") {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	var out strings.Builder
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		out.WriteString(filepath.ToSlash(f) + " " + hex.EncodeToString(sum[:8]) + "\n")
	}
	return out.String()
}

// campTasksTreeDiff names the files that differ between two
// campTasksTreeHash results (added, removed or changed).
func campTasksTreeDiff(before, after string) string {
	in := func(list, line string) bool { return strings.Contains("\n"+list, "\n"+line+"\n") }
	var d []string
	for _, l := range strings.Split(strings.TrimSpace(before), "\n") {
		if !in(after, l) {
			d = append(d, "- "+l)
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(after), "\n") {
		if !in(before, l) {
			d = append(d, "+ "+l)
		}
	}
	return strings.Join(d, "; ")
}
