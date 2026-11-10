package wire

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
)

// Ids of the v0.1.17 shared lore.db fixture: project A is migrated,
// project B shares the file and must never leak into A's store.
const (
	v17ProjA    = "prj_01a1af104e66762ab4fe145c43aba9d8"
	v17ProjB    = "prj_01a1af104e66762ab4fe145c43aba9d9"
	v17MemA     = "mem_01a1af106a357424ba3b084bd5010e36"
	v17MemB     = "mem_01a1af106a357424ba3b084bd5010e37"
	v17Tag      = "tag_1092b25e5b3072429ed1bccebcf4af8b"
	v17TaskA    = "tsk_01a1af104f597542ba6533344ceb3cb3"
	v17CmtA     = "cmt_01a1af108f8670868b2d8157445678ee"
	v17CmtB     = "cmt_01a1af108f8670868b2d8157445678ef"
	v17GoTime   = "2026-11-06 04:14:10.997235823 +0000 UTC m=+0.096559973" // ent via modernc
	v17WantTime = "2026-11-06T04:14:10.997235823Z"
)

// v017SharedDB writes a shared lore.db with v0.1.17's column shapes (the
// tables a v0.1.17 `lore project shared-init` + memory/tag/task/comment
// add leave, columns as ent created them) at path.
func v017SharedDB(t *testing.T, path string) {
	t.Helper()
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	db, err := sql.Open(sqliteDriver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }() // test fixture: a close error shows up in the reads that follow
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, created_at datetime NOT NULL, updated_at datetime NOT NULL, name TEXT NOT NULL, origin_url TEXT, archived_at datetime, last_active_at datetime NOT NULL)`,
		`CREATE TABLE memories (id TEXT PRIMARY KEY, created_at datetime NOT NULL, updated_at datetime NOT NULL, trust_score REAL NOT NULL, confidence REAL, last_accessed_at datetime, source_kind TEXT NOT NULL, kind TEXT NOT NULL, body TEXT NOT NULL, tx_at datetime NOT NULL, embedding_dim INTEGER, embedding BLOB, project_id TEXT NOT NULL, superseded_by_id TEXT)`,
		`CREATE TABLE tags (id TEXT PRIMARY KEY, created_at datetime NOT NULL, updated_at datetime NOT NULL, project_id TEXT NOT NULL, name TEXT NOT NULL, color TEXT)`,
		`CREATE TABLE entity_tags (id TEXT PRIMARY KEY, created_at datetime NOT NULL, updated_at datetime NOT NULL, entity_table TEXT NOT NULL, entity_id TEXT NOT NULL, tag_id TEXT NOT NULL)`,
		`CREATE TABLE comments (id TEXT PRIMARY KEY, created_at datetime NOT NULL, updated_at datetime NOT NULL, entity_table TEXT NOT NULL, entity_id TEXT NOT NULL, entity_project_id TEXT, body TEXT NOT NULL, created_by_actor_id TEXT)`,
		`CREATE TABLE tasks (id TEXT PRIMARY KEY, created_at datetime NOT NULL, updated_at datetime NOT NULL, project_id TEXT NOT NULL, title TEXT NOT NULL, body TEXT, status TEXT NOT NULL, priority TEXT NOT NULL, commitment TEXT NOT NULL, blocked_by json, claim_epoch INTEGER NOT NULL, attempts INTEGER NOT NULL, claim_log json, done_pr INTEGER)`,
	}
	ins := []struct {
		q    string
		args []any // database/sql's Exec takes ...any
	}{
		{`INSERT INTO projects VALUES (?, ?, ?, ?, NULL, NULL, ?)`, []any{v17ProjA, v17GoTime, v17GoTime, "infra", v17GoTime}},
		{`INSERT INTO projects VALUES (?, ?, ?, ?, NULL, NULL, ?)`, []any{v17ProjB, v17GoTime, v17GoTime, "other", v17GoTime}},
		{`INSERT INTO memories VALUES (?, ?, ?, 0.5, NULL, ?, 'manual', 'retrieved', 'the staging cluster sleeps', ?, 3, x'0102', ?, NULL)`, []any{v17MemA, v17GoTime, "2026-11-06 04:14:11", v17GoTime, v17GoTime, v17ProjA}},
		{`INSERT INTO memories VALUES (?, ?, ?, 0.5, NULL, NULL, 'manual', 'retrieved', 'other project note', ?, NULL, NULL, ?, NULL)`, []any{v17MemB, v17GoTime, v17GoTime, v17GoTime, v17ProjB}},
		{`INSERT INTO tags VALUES (?, ?, ?, ?, 'ops', NULL)`, []any{v17Tag, v17GoTime, v17GoTime, v17ProjA}},
		{`INSERT INTO entity_tags VALUES ('etg_1', ?, ?, 'memories', ?, ?)`, []any{v17GoTime, v17GoTime, v17MemA, v17Tag}},
		{`INSERT INTO entity_tags VALUES ('etg_2', ?, ?, 'memories', ?, ?)`, []any{v17GoTime, v17GoTime, v17MemB, v17Tag}},
		// v0.1.17's comment add left entity_project_id NULL
		{`INSERT INTO comments VALUES (?, ?, ?, 'memories', ?, NULL, 'agreed', NULL)`, []any{v17CmtA, v17GoTime, v17GoTime, v17MemA}},
		{`INSERT INTO comments VALUES (?, ?, ?, 'memories', ?, NULL, 'not yours', NULL)`, []any{v17CmtB, v17GoTime, v17GoTime, v17MemB}},
		{`INSERT INTO tasks VALUES (?, ?, ?, ?, 'fix the build', NULL, 'in_progress', 'medium', 'accepted', ?, 2, 0, NULL, NULL)`, []any{v17TaskA, v17GoTime, v17GoTime, v17ProjA, `[ "tsk_01a1af104f597542ba6533344ceb3cb4" ]`}},
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	for _, i := range ins {
		if _, err := db.Exec(i.q, i.args...); err != nil {
			t.Fatalf("%s: %v", i.q, err)
		}
	}
}

// v017ModeBRepo is a git repo holding only a Mode B pointer (pointerName,
// body) and a shared lore.db built by v017SharedDB under shared.
func v017ModeBRepo(t *testing.T, pointerName, body string) (repo, shared, pointer string) {
	t.Helper()
	repo = gitRepo(t)
	shared = t.TempDir()
	v017SharedDB(t, filepath.Join(shared, "lore.db"))
	pointer = filepath.Join(repo, ".lore", pointerName)
	if err := os.MkdirAll(filepath.Dir(pointer), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pointer, []byte(strings.ReplaceAll(body, "SHARED", shared)), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo, shared, pointer
}

// exportDocs decodes an export's documents by id.
func exportDocs(t *testing.T, exp ModeBExport) map[string]map[string]json.RawMessage {
	t.Helper()
	out := map[string]map[string]json.RawMessage{}
	for _, d := range exp.Docs {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(d, &m); err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		var id string
		if err := json.Unmarshal(m["id"], &id); err != nil {
			t.Fatal(err)
		}
		out[id] = m
	}
	return out
}

// TestV017ModeBExport: the project's rows only (another project's memory
// and its comment stay behind), `_v: 1` documents typed by the table
// definition (Go time.String() cells → row-file times, REAL → number, JSON
// text → compacted JSON, volatile columns such as embedding and
// last_accessed_at left out), a comment with no entity_project_id moves
// with its memory, and only the exported rows' entity_tags become links.
func TestV017ModeBExport(t *testing.T) {
	t.Parallel()
	for _, form := range []struct{ name, file, body string }{
		{"yaml", "lore.yaml", "db_path: SHARED/lore.db\nproject_id: " + v17ProjA + "\n"},
		{"toml", "lore.toml", "# v0.1.17 pointer\ndb_path = \"SHARED/lore.db\"\nproject_id = '" + v17ProjA + "' # kept\n"},
	} {
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()
			_, shared, pointer := v017ModeBRepo(t, form.file, form.body)
			exp, err := V017ModeB{}.ExportModeB(context.Background(), pointer)
			if err != nil {
				t.Fatal(err)
			}
			if exp.ProjectName != "infra" || exp.ProjectID != v17ProjA || exp.DBPath != filepath.Join(shared, "lore.db") {
				t.Fatalf("export header %+v", exp)
			}
			docs := exportDocs(t, exp)
			got := make([]string, 0, len(docs))
			for id := range docs {
				got = append(got, id)
			}
			slices.Sort(got)
			want := []string{v17CmtA, v17MemA, v17Tag, v17TaskA}
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("exported %v, want %v", got, want)
			}
			mem := docs[v17MemA]
			for k, w := range map[string]string{"_v": "1", "_table": `"memories"`, "created_at": `"` + v17WantTime + `"`, "tx_at": `"` + v17WantTime + `"`, "trust_score": "0.5", "confidence": "null", "body": `"the staging cluster sleeps"`} {
				if string(mem[k]) != w {
					t.Fatalf("memory %s = %s, want %s (doc %v)", k, mem[k], w, mem)
				}
			}
			for _, k := range []string{"embedding", "embedding_dim", "last_accessed_at"} {
				if _, has := mem[k]; has {
					t.Fatalf("memory carries the volatile column %s", k)
				}
			}
			task := docs[v17TaskA]
			if string(task["blocked_by"]) != `["tsk_01a1af104f597542ba6533344ceb3cb4"]` || string(task["claim_epoch"]) != "2" || string(task["claim_log"]) != "null" {
				t.Fatalf("task %v", task)
			}
			if string(docs[v17CmtA]["entity_id"]) != `"`+v17MemA+`"` {
				t.Fatalf("comment %v", docs[v17CmtA])
			}
			if len(exp.TagLinks) != 1 || exp.TagLinks[0] != (opsmemory.TagLink{EntityTable: "memories", EntityID: v17MemA, TagID: v17Tag}) {
				t.Fatalf("tag links %+v", exp.TagLinks)
			}
		})
	}
}

// TestV017ModeBExportRefusals: a pointer that is not one, a missing shared
// file, a project not in it and a ".." path each answer their code.
func TestV017ModeBExportRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, file, body string
		want             errs.Code
	}{
		{"yaml without project_id", "lore.yaml", "db_path: SHARED/lore.db\n", errs.InvalidInput},
		{"yaml not a map", "lore.yaml", "- a\n", errs.InvalidInput},
		{"toml no equals", "lore.toml", "db_path SHARED/lore.db\n", errs.InvalidInput},
		{"toml number", "lore.toml", "db_path = 3\nproject_id = \"" + v17ProjA + "\"\n", errs.InvalidInput},
		{"toml trailing text", "lore.toml", "db_path = \"SHARED/lore.db\" x\nproject_id = \"" + v17ProjA + "\"\n", errs.InvalidInput},
		{"toml open literal", "lore.toml", "db_path = 'SHARED/lore.db\nproject_id = \"" + v17ProjA + "\"\n", errs.InvalidInput},
		{"missing db", "lore.yaml", "db_path: SHARED/gone.db\nproject_id: " + v17ProjA + "\n", errs.NotFound},
		{"unknown project", "lore.yaml", "db_path: SHARED/lore.db\nproject_id: prj_01a1af104e66762ab4fe145c43abffff\n", errs.NotFound},
		{"dot dot", "lore.yaml", "db_path: SHARED/../x/lore.db\nproject_id: " + v17ProjA + "\n", errs.BadPath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, pointer := v017ModeBRepo(t, tc.file, tc.body)
			if _, err := (V017ModeB{}).ExportModeB(context.Background(), pointer); !errs.Has(err, tc.want) {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
		})
	}
}

// TestExpandModeBPath: v0.1.17's placeholders and its ".." refusal.
func TestExpandModeBPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct {
		name, in, loreHome, allow, want string
		bad                             bool
	}{
		{name: "plain", in: "/data/lore.db", want: "/data/lore.db"},
		{name: "relative kept", in: "db/lore.db", want: "db/lore.db"},
		{name: "home", in: "${HOME}/s/lore.db", want: home + "/s/lore.db"},
		{name: "lore home default", in: "${LORE_HOME}/lore.db", want: home + "/.lore/lore.db"},
		{name: "lore home set", in: "${LORE_HOME}/lore.db", loreHome: "/lh", want: "/lh/lore.db"},
		{name: "project root", in: "${PROJECT_ROOT}/x.db", want: "/repo/x.db"},
		{name: "dot dot refused", in: "/a/../b.db", bad: true},
		{name: "dot dot via placeholder refused", in: "${PROJECT_ROOT}/../b.db", bad: true},
		{name: "dot dot allowed", in: "/a/../b.db", allow: envOn, want: "/a/../b.db"},
		{name: "dots inside a name fine", in: "/a/..b/c.db", want: "/a/..b/c.db"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(modeBLoreHomeEnv, tc.loreHome)
			t.Setenv(modeBAllowOutsideEnv, tc.allow)
			got, err := expandModeBPath(tc.in, "/repo")
			if tc.bad {
				if !errs.Has(err, errs.BadPath) {
					t.Fatalf("got %q, %v; want E_BAD_PATH", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// TestV017Cell: every field kind against every cell shape json_object
// gives (NULL, number, text), and the cells a kind refuses.
func TestV017Cell(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind    value.Kind
		cell    string
		want    string
		wantErr bool
	}{
		{kind: value.KindText, cell: `null`, want: `null`},
		{kind: value.KindText, cell: ``, want: `null`},
		{kind: value.KindText, cell: `"a \"q\" ü"`, want: `"a \"q\" ü"`},
		{kind: value.KindText, cell: `42`, want: `"42"`},
		{kind: value.KindRef, cell: `"mem_x"`, want: `"mem_x"`},
		{kind: value.KindBool, cell: `1`, want: `true`},
		{kind: value.KindBool, cell: `0`, want: `false`},
		{kind: value.KindBool, cell: `"true"`, want: `true`},
		{kind: value.KindBool, cell: `2`, wantErr: true},
		{kind: value.KindBool, cell: `"yes"`, wantErr: true},
		{kind: value.KindNumber, cell: `0.5`, want: `0.5`},
		{kind: value.KindNumber, cell: `"7"`, want: `7`},
		{kind: value.KindNumber, cell: `"seven"`, wantErr: true},
		{kind: value.KindDateTime, cell: `"` + v17GoTime + `"`, want: `"` + v17WantTime + `"`},
		{kind: value.KindDateTime, cell: `"2026-11-06 04:14:11"`, want: `"2026-11-06T04:14:11.000000000Z"`},
		{kind: value.KindDateTime, cell: `"2026-11-06T09:44:11.5+05:30"`, want: `"2026-11-06T04:14:11.500000000Z"`},
		{kind: value.KindDateTime, cell: `"2026-11-06 04:14:11.25-02:00"`, want: `"2026-11-06T06:14:11.250000000Z"`},
		{kind: value.KindDateTime, cell: `"yesterday"`, wantErr: true},
		{kind: value.KindDateTime, cell: `17`, wantErr: true},
		{kind: value.KindJSON, cell: `"{ \"a\": [1, 2] }"`, want: `{"a":[1,2]}`},
		{kind: value.KindJSON, cell: `"{bad"`, wantErr: true},
		{kind: value.KindJSON, cell: `3`, want: `3`},
		{kind: value.KindList, cell: `"[ \"x\", \"y\" ]"`, want: `["x","y"]`},
		{kind: value.KindList, cell: `null`, want: `null`},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind)+" "+tc.cell, func(t *testing.T) {
			got, err := v017Cell(tc.kind, json.RawMessage(tc.cell))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got %s, want an error", got)
				}
				return
			}
			if err != nil || string(got) != tc.want {
				t.Fatalf("got %s, %v; want %s", got, err, tc.want)
			}
		})
	}
}

// TestV017ModeBMigrateRun: `lore migrate run` with the standalone
// exporter converts a real v0.1.17 shared lore.db: _v 2 row files in
// <db dir>/stores/infra, the tag link folded into the memory's tag_ids,
// the comment moved with its memory, nothing of project B, the rows
// readable through the store, and a second run a no-op.
func TestV017ModeBMigrateRun(t *testing.T) {
	t.Parallel()
	repo, shared, pointer := v017ModeBRepo(t, "lore.yaml", "db_path: SHARED/lore.db\nproject_id: "+v17ProjA+"\n")
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	open := func() *App {
		t.Helper()
		app, nerr := New(Options{RepoDir: repo, Home: home, Actor: actor, ModeB: V017ModeB{}})
		if nerr != nil {
			t.Fatal(nerr)
		}
		t.Cleanup(func() { _ = app.Close() }) // test teardown
		return app
	}
	raw, err := run(t, open(), opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := migrateOut(t, raw); got.Count != 4 {
		t.Fatalf("run: %+v, want 4 rows", got)
	}
	data := filepath.Join(shared, modeBStoresDir, "infra", ".lore", "data")
	for _, c := range []struct{ table, id string }{{"memories", v17MemA}, {"tags", v17Tag}, {"tasks", v17TaskA}, {"comments", v17CmtA}} {
		f := rowFile(t, filepath.Join(data, c.table, c.id+".json"))
		if string(f["_v"]) != "2" || len(f["_hlc"]) == 0 {
			t.Fatalf("%s/%s is not a _v 2 row: %v", c.table, c.id, f)
		}
	}
	if f := rowFile(t, filepath.Join(data, "memories", v17MemA+".json")); !strings.Contains(string(f["tag_ids"]), v17Tag) {
		t.Fatalf("memory tag_ids = %s, want %s", f["tag_ids"], v17Tag)
	}
	for _, gone := range []string{filepath.Join(data, "memories", v17MemB+".json"), filepath.Join(data, "comments", v17CmtB+".json")} {
		if _, serr := os.Stat(gone); !os.IsNotExist(serr) {
			t.Fatalf("project B's row %s reached A's store: %v", gone, serr)
		}
	}
	if _, serr := os.Stat(pointer + ModeBMigratedSuffix); serr != nil {
		t.Fatalf("pointer not kept as .migrated: %v", serr)
	}
	b := open()
	out, err := run(t, b, opsmemory.OpList, `{"store":"`+ModeBStoreName+`"}`, false)
	if err != nil || !strings.Contains(out, v17MemA) || strings.Contains(out, v17MemB) {
		t.Fatalf("memory list --store shared: %s %v", out, err)
	}
	raw, err = run(t, b, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if again := migrateOut(t, raw); again.Count != 0 || len(again.Steps) != 0 {
		t.Fatalf("second run must be a no-op: %+v", again)
	}
}

// TestStandaloneOptionsReadModeB: cmd/lore's options carry the v0.1.17
// reader, so `lore migrate run` converts a Mode B repo on the release
// binary instead of answering "this build cannot read the shared lore.db".
func TestStandaloneOptionsReadModeB(t *testing.T) {
	t.Parallel()
	o := StandaloneOptions(t.TempDir(), t.TempDir(), "", "test")
	if _, ok := o.ModeB.(V017ModeB); !ok {
		t.Fatalf("StandaloneOptions ModeB = %T, want V017ModeB", o.ModeB)
	}
}
