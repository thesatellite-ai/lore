package wire

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// v0.1.17's DDL of the built-in machine-local tables P3-LOCAL-5 keeps, as
// ent created them (dbent/schema/sessions.go, render_history.go,
// tech_doc_pages.go plus AicoderBaseMixin's envelope columns).
const (
	v017SessionsDDL = `CREATE TABLE sessions (id TEXT PRIMARY KEY, created_at DATETIME, updated_at DATETIME, project_id TEXT NOT NULL,
		started_at DATETIME NOT NULL, ended_at DATETIME, actor_id TEXT, agent_kind TEXT, tool TEXT NOT NULL DEFAULT '',
		tool_session_id TEXT NOT NULL DEFAULT '', tool_version TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
		machine_id TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '', repo TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL DEFAULT '', terminal TEXT NOT NULL DEFAULT '', ci TEXT NOT NULL DEFAULT '', parent_session_id TEXT NOT NULL DEFAULT '',
		signal TEXT NOT NULL DEFAULT '', sources JSON, outer_tools JSON, declared_human BOOL NOT NULL DEFAULT false, flags JSON)`
	v017RenderHistoriesDDL = `CREATE TABLE render_histories (id TEXT PRIMARY KEY, created_at DATETIME, updated_at DATETIME, project_id TEXT NOT NULL,
		repo_id TEXT, target_path TEXT NOT NULL, rendered_text TEXT NOT NULL, rendered_sha256 TEXT NOT NULL, total_bytes INTEGER NOT NULL,
		total_tokens INTEGER, scope_summary TEXT NOT NULL, rendered_by_actor_id TEXT)`
	v017TechDocPagesDDL = `CREATE TABLE tech_doc_pages (id TEXT PRIMARY KEY, created_at DATETIME, updated_at DATETIME, project_id TEXT NOT NULL,
		tech_doc_id TEXT NOT NULL, url TEXT NOT NULL, title TEXT, body TEXT NOT NULL, content_sha TEXT)`
)

// Ids of the v0.1.17 rows TestCopyMachineLocalBuiltins copies.
const (
	copySession = "ses_0190e6a0c7d27b4c8a1e2f3a4b5c6d70"
	copyRender  = "rnd_0190e6a0c7d27b4c8a1e2f3a4b5c6d71"
	copyPage    = "tdp_0190e6a0c7d27b4c8a1e2f3a4b5c6d72"
)

// TestCopyMachineLocalBuiltins: the real sessions, render_histories and
// tech_doc_pages defs copy a v0.1.17 lore.db column for column with no new
// code (P3-LOCAL-4/5): JSON columns stay JSON, the 0/1 bool and ent's
// integer bytes convert, the raw tool session id is kept (the row exists
// to hold it, OQ14), and a rerun copies nothing.
func TestCopyMachineLocalBuiltins(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lore.db")
	const at = "'2026-01-02 03:04:05.123456789+00:00'"
	writeV017DB(t, dbPath, v017SessionsDDL, v017RenderHistoriesDDL, v017TechDocPagesDDL,
		`INSERT INTO sessions VALUES ('`+copySession+`', `+at+`, `+at+`, 'prj_x', `+at+`, `+at+`, 'act_a', 'codex', 'codex',
			'0199aaaa-bbbb-cccc-dddd-eeeeffff0000', '0.5.1', 'gpt-5', 'mch_1', '', 'lore', 'main', 'saas', 'xterm', '', '',
			'CODEX_THREAD_ID', '{"model":"session_hook"}', '["tmux"]', 1, '["tty"]')`,
		`INSERT INTO render_histories VALUES ('`+copyRender+`', `+at+`, `+at+`, 'prj_x', 'rep_1', '.lore/LORE.md', '# lore',
			'abc123', 6, NULL, '{"included":{"rules":1},"cut_summary":{}}', 'act_a')`,
		`INSERT INTO tech_doc_pages VALUES ('`+copyPage+`', `+at+`, `+at+`, 'prj_x', 'tdc_1', 'https://example.com/a', NULL, 'page body', 'sha')`)
	eng := newCopyEngineOver(t, filepath.Join(dir, "home"), schema.Builtin())

	steps, n, err := copyMachineLocal(ctx, eng, dbPath, false)
	if err != nil || n != 3 || len(steps) != 3 {
		t.Fatalf("copy = %q, %d, %v; want one row of each table", steps, n, err)
	}
	want := map[string]map[string]string{
		copySession: {schema.FieldSessionToolSessionID: "0199aaaa-bbbb-cccc-dddd-eeeeffff0000", schema.FieldSessionModel: "gpt-5",
			schema.FieldSessionSources: `{"model":"session_hook"}`, schema.FieldSessionOuterTools: "tmux",
			schema.FieldSessionDeclaredHuman: "true", schema.FieldSessionFlags: "tty", schema.FieldSessionStartedAt: "2026-01-02T03:04:05.123456789Z"},
		copyRender: {schema.FieldRenderTarget: ".lore/LORE.md", schema.FieldRenderBytes: "6", schema.FieldRenderScopeSummary: `{"included":{"rules":1},"cut_summary":{}}`},
		copyPage:   {schema.FieldTechDocPageTechDocID: "tdc_1", schema.FieldTechDocPageBody: "page body"},
	}
	for id, fields := range want {
		ref, perr := ids.ParseAny(id)
		if perr != nil {
			t.Fatal(perr)
		}
		src, r, gerr := eng.Get(ctx, engine.Ref{ID: ref})
		if gerr != nil || src != store.SourceMachine {
			t.Fatalf("%s: Get = %s, %v; want the machine store", id, src, gerr)
		}
		for k, w := range fields {
			if v, ok := r.Fields[k]; !ok || v.String() != w {
				t.Errorf("%s field %s = %v, want %s", id, k, v, w)
			}
		}
	}
	if _, ok := mustGet(ctx, t, eng, copyPage).Fields[schema.FieldTechDocPageTitle]; ok {
		t.Error("a NULL title was copied")
	}
	if steps, n, err = copyMachineLocal(ctx, eng, dbPath, false); err != nil || n != 0 || len(steps) != 0 {
		t.Fatalf("rerun = %q, %d, %v; want nothing", steps, n, err)
	}
}

// mustGet reads the row id through eng.
func mustGet(ctx context.Context, t *testing.T, eng *engine.Engine, id string) row.Row {
	t.Helper()
	ref, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := eng.Get(ctx, engine.Ref{ID: ref})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
