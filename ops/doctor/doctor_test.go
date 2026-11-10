package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

type fakeHost struct {
	base   Base
	gotFix bool
}

func (h *fakeHost) Base(_ context.Context, fix bool) (Base, error) {
	h.gotFix = fix
	return h.base, nil
}

type fakeEnv struct {
	host   *fakeHost
	runs   []CheckRun
	gotFix bool
}

func (e *fakeEnv) DoctorHost(context.Context) (Host, bool) {
	if e.host == nil {
		return nil, false
	}
	return e.host, true
}

func (e *fakeEnv) DoctorChecks(_ context.Context, fix bool) []CheckRun {
	e.gotFix = fix
	return e.runs
}
func (*fakeEnv) BinaryVersion() string { return "v9.9.9" }

func healthy() Base {
	return Base{Status: StatusHealthy, DBOK: true, Identity: Identity{Resolved: "git:mira@acme.example", Source: "git-email", Stable: true}}
}

// The status × core-check matrix: the exit status follows the status,
// a warning not fixed degrades, a fixed warning or a note does not, a
// check that cannot run degrades, and broken is never raised to degraded.
func TestStatusMatrix(t *testing.T) {
	warn := Finding{Check: "task_lead", Level: LevelWarning, Line: "task_lead: tsk_1 has no lead"}
	fixed := Finding{Check: "task_lead", Level: LevelWarning, Fixed: true, Line: "task_lead: tsk_1 (fixed)"}
	note := Finding{Check: "internal_off", Level: LevelNote, Line: "internal_off: note"}
	cases := []struct {
		name       string
		base       Health
		runs       []CheckRun
		wantStatus Health
		wantExit   int
		wantWarns  int
		wantNotes  int
	}{
		{"healthy", StatusHealthy, nil, StatusHealthy, 0, 0, 0},
		{"note only", StatusHealthy, []CheckRun{{Check: "internal_off", Findings: []Finding{note}}}, StatusHealthy, 0, 0, 1},
		{"fixed warning", StatusHealthy, []CheckRun{{Check: "task_lead", Findings: []Finding{fixed}}}, StatusHealthy, 0, 0, 1},
		{"warning", StatusHealthy, []CheckRun{{Check: "task_lead", Findings: []Finding{warn}}}, StatusDegraded, 1, 1, 0},
		{"check failed", StatusHealthy, []CheckRun{{Check: "store_lock", Err: errors.New("boom")}, {Check: "internal_off", Findings: []Finding{note}}}, StatusDegraded, 1, 1, 1},
		// A base status with no line of its own is listed (unnamedBase), never left unexplained.
		{"degraded base", StatusDegraded, nil, StatusDegraded, 1, 1, 0},
		{"broken stays broken", StatusBroken, []CheckRun{{Check: "task_lead", Findings: []Finding{warn}}}, StatusBroken, 2, 1, 0},
	}
	for _, c := range cases {
		b := healthy()
		b.Status = c.base
		env := &fakeEnv{host: &fakeHost{base: b}, runs: c.runs}
		r, err := Run(context.Background(), env, RunIn{})
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != c.wantStatus || r.ExitStatus() != c.wantExit || r.Exit != c.wantExit || r.OK != (c.wantStatus == StatusHealthy) ||
			len(r.Warnings) != c.wantWarns || len(r.Notes) != c.wantNotes {
			t.Fatalf("%s: %+v", c.name, r)
		}
	}
}

// --fix reaches both the host and the core checks.
func TestFixReachesBoth(t *testing.T) {
	env := &fakeEnv{host: &fakeHost{base: healthy()}}
	if _, err := Run(context.Background(), env, RunIn{Fix: true}); err != nil {
		t.Fatal(err)
	}
	if !env.gotFix || !env.host.gotFix {
		t.Fatalf("fix: core %v host %v", env.gotFix, env.host.gotFix)
	}
}

// Without lore.db (cmd/lore) the core checks still run and the report
// says what was not checked.
func TestNoHost(t *testing.T) {
	env := &fakeEnv{runs: []CheckRun{{Check: "store_lock", Findings: []Finding{{Level: LevelWarning, Line: "store_lock: held"}}}}}
	r, err := Run(context.Background(), env, RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusDegraded || r.Exit != 1 || r.Notes[0] != noLoreDBNote || r.BinaryVersion != "v9.9.9" {
		t.Fatalf("no host: %+v", r)
	}
	if text := r.Text(); strings.Contains(text, "[db]") || strings.Contains(text, "[identity]") || !strings.Contains(text, "Warnings:\n  - store_lock: held\n") {
		t.Fatalf("no host text:\n%s", text)
	}
}

// The JSON keeps v0.1.17's keys, adds exit_status, and nests the sync
// section; the text keeps the v0.1.17 layout.
func TestJSONAndText(t *testing.T) {
	b := healthy()
	b.Sync = &Sync{Baselined: true, PendingExports: 2, FileErrors: []FileError{{Path: "a", Kind: "shape"}}}
	b.Warnings = []string{"w1"}
	b.Status = StatusDegraded
	r, err := Run(context.Background(), &fakeEnv{host: &fakeHost{base: b}}, RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"schema_version", "ok", "status", "db_ok", "wal_size_bytes", "identity", "warnings", "binary_version", "sync", "exit_status"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing %s in %s", k, data)
		}
	}
	if string(m["exit_status"]) != "1" || !strings.Contains(string(m["sync"]), `"pending_exports":2`) {
		t.Fatalf("json: %s", data)
	}
	text := r.Text()
	for _, want := range []string{"Status: DEGRADED\n", "[sync]\n  baselined:        true\n", "  file errors:      1\n", "[identity]\n", "\nWarnings:\n  - w1\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text missing %q:\n%s", want, text)
		}
	}
}

func TestOpsSpec(t *testing.T) {
	reg, err := op.NewRegistry(Ops(&fakeEnv{})...)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := reg.Lookup(OpRun)
	if !ok || !e.Spec().Local {
		t.Fatalf("spec: %v %+v", ok, e.Spec())
	}
}

// TestSyncWithoutLoreDB (E-GAPS-1 G4): a host without lore.db prints the
// row-file lines of [sync] (in git repo, tracked files, data gitignored),
// never the lore.db ones (baselined, pending exports), and its JSON has
// no lore.db key. Break-verified: printing the old lines for every host
// fails the text half; dropping omitempty fails the JSON half.
func TestSyncWithoutLoreDB(t *testing.T) {
	b := healthy()
	b.Sync = &Sync{NoLoreDB: true, InGitRepo: true, DataDirPresent: true, TrackedFiles: 4, OpenConflicts: 1, ProjectFiles: 1,
		FileErrors: []FileError{{Path: ".lore/data/memories/x.json", Kind: FileErrorConflict}}, Uncommitted: 2, MergeDriverOK: true}
	r, err := Run(context.Background(), &fakeEnv{host: &fakeHost{base: b}}, RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	var s map[string]json.RawMessage
	if err = json.Unmarshal(m["sync"], &s); err != nil {
		t.Fatalf("sync key: %v in %s", err, data)
	}
	want := map[string]string{"in_git_repo": "true", "data_dir_present": "true", "tracked_files": "4", "open_conflicts": "1",
		"project_files": "1", "uncommitted_files": "2", "merge_driver_configured": "true", "data_dir_gitignored": "false"}
	for k, v := range want {
		if string(s[k]) != v {
			t.Errorf("sync.%s = %s, want %s (%s)", k, s[k], v, m["sync"])
		}
	}
	for _, k := range []string{"baselined", "pending_exports", "trash_rows", "errors", "dangling_references", "duplicate_groups", "NoLoreDB"} {
		if _, ok := s[k]; ok {
			t.Errorf("sync carries %s: %s", k, m["sync"])
		}
	}
	text := r.Text()
	wantText := "[sync]\n  in git repo:      true\n  tracked files:    4\n  open conflicts:   1\n  file errors:      1\n  merge driver:     true\n  data gitignored:  false\n  uncommitted:      2\n\n"
	if !strings.Contains(text, wantText) || strings.Contains(text, "baselined") || strings.Contains(text, "pending exports") {
		t.Errorf("text:\n%s\nwant block:\n%s", text, wantText)
	}
}
