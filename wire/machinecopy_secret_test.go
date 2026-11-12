package wire

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// v0.1.17's DDL of runs (P3-RUN-1 keeps it column for column).
const v017RunsDDL = `CREATE TABLE runs (id TEXT PRIMARY KEY, created_at DATETIME, updated_at DATETIME, project_id TEXT,
	kind TEXT NOT NULL, status_str TEXT NOT NULL, actor_id TEXT, started_at DATETIME, completed_at DATETIME, notes TEXT)`

// Ids of the v0.1.17 runs rows TestCopyMachineLocalSecretRedacted copies.
const (
	copyRunSecret = "run_0190e6a0c7d27b4c8a1e2f3a4b5c6d80"
	copyRunPlain  = "run_0190e6a0c7d27b4c8a1e2f3a4b5c6d81"
)

// TestCopyMachineLocalSecretRedacted is item 47: a v0.1.17 runs row whose
// notes hold a generic-api-key-looking value (assembled here, never a
// literal) does not stop the copy under the default on_detect (ask) with
// no choice given: the row lands with the match as ${secret:unstored},
// one step line names table, id, field, rule and the match hash and never
// any part of the value, the other rows land in one staged write with no
// store-then-rotate task, and a rerun copies nothing.
func TestCopyMachineLocalSecretRedacted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lore.db")
	secret := strings.Join([]string{"Zk3Qp9Lm2Xv8", "Rt5Wn7Yb4Hc6", "Jd1Fg0Ks"}, "")
	notes := `{"api_key": "` + secret + `"}`
	const at = "'2026-01-02 03:04:05.123456789+00:00'"
	writeV017DB(t, dbPath, v017RunsDDL, v017RenderHistoriesDDL,
		`INSERT INTO runs VALUES ('`+copyRunSecret+`', `+at+`, `+at+`, 'prj_x', 'agent', 'success', 'act_a', `+at+`, `+at+`, '`+notes+`')`,
		`INSERT INTO runs VALUES ('`+copyRunPlain+`', `+at+`, `+at+`, 'prj_x', 'agent', 'failed', 'act_a', `+at+`, NULL, '{"steps":3}')`,
		`INSERT INTO render_histories VALUES ('`+copyRender+`', `+at+`, `+at+`, 'prj_x', 'rep_1', '.lore/LORE.md', '# lore',
			'abc123', 6, NULL, '{"included":{"rules":1},"cut_summary":{}}', 'act_a')`)
	base, err := gitleaks.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	rules, err := gitleaks.Rules(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := gitleaks.New(rules)
	if err != nil {
		t.Fatal(err)
	}
	eng := newCopyEngineScan(t, filepath.Join(dir, "home"), schema.Builtin(), sc)
	// the store-then-rotate task is a shared-table row the machine store
	// cannot hold (a real run failed at the stage's commit): never asked for
	eng.SetRedactHook(func(context.Context, engine.RedactedWrite) error {
		return errors.New("the redact task ran for a machine-local copy")
	})

	steps, n, err := copyMachineLocal(ctx, eng, dbPath, false)
	if err != nil || n != 3 {
		t.Fatalf("copy = %q, %d, %v; want all 3 rows copied", steps, n, err)
	}
	all := strings.Join(steps, "\n")
	for i := 0; i+4 <= len(secret); i++ {
		if strings.Contains(all, secret[i:i+4]) {
			t.Fatalf("a step line shows part of the value: %q", steps)
		}
	}
	var line string
	for _, s := range steps {
		if strings.Contains(s, "redacted runs row "+copyRunSecret) {
			line = s
		}
	}
	for _, w := range []string{StepMachineLocal + ": redacted runs row " + copyRunSecret, "field notes", gitleaks.RuleGenericAPIKey, "sha256:", refs.Unstored, "lore secrets link " + copyRunSecret} {
		if !strings.Contains(line, w) {
			t.Fatalf("redacted line %q lacks %q (steps %q)", line, w, steps)
		}
	}
	for id, want := range map[string]string{copyRunSecret: refs.Unstored, copyRunPlain: `{"steps":3}`, copyRender: "# lore"} {
		ref, perr := ids.ParseAny(id)
		if perr != nil {
			t.Fatal(perr)
		}
		src, r, gerr := eng.Get(ctx, engine.Ref{ID: ref})
		if gerr != nil || src != store.SourceMachine {
			t.Fatalf("%s: Get = %s, %v; want the machine store", id, src, gerr)
		}
		f := schema.FieldRunNotes
		if id == copyRender {
			f = schema.FieldRenderText
		}
		got := r.Fields[f].String()
		if !strings.Contains(got, want) || strings.Contains(got, secret) {
			t.Fatalf("%s.%s = %q, want it to hold %q and not the value", id, f, got, want)
		}
	}
	if steps, n, err = copyMachineLocal(ctx, eng, dbPath, false); err != nil || n != 0 || len(steps) != 0 {
		t.Fatalf("rerun = %q, %d, %v; want nothing", steps, n, err)
	}
}
