package lsync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dbent"
	"dbent/gen/ent"
	"dbent/pkg/dbtemplate"

	"saas/pkg/aicoder/canonjson"
)

// templateDB is a migrated DB built once in TestMain and copied per test
// (see dbent/pkg/dbtemplate for why migrations must not run in parallel).
var templateDB string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests builds the template, runs the tests and cleans up (split from
// TestMain because os.Exit skips deferred calls).
func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "lsync-template-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir) // temp fixture; nothing to report on failure
	templateDB = filepath.Join(dir, "template.db")
	if err := dbtemplate.Build(context.Background(), templateDB); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

// env is one simulated clone: a migrated lore.db plus its .lore/data dir.
type env struct {
	t       *testing.T
	dir     string
	data    string
	db      *sql.DB
	client  *ent.Client
	project string
	actor   string
}

// newEnv creates a migrated DB with one project and one actor, like `lore init`.
func newEnv(t *testing.T) *env {
	t.Helper()
	e := newEmptyEnv(t)
	ctx := context.Background()
	a, err := e.client.Actor.Create().SetKind("human").SetDisplayName("dev").
		SetStableKey("human:dev-" + filepath.Base(e.dir) + "@example.com").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.client.Project.Create().SetName("proj").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.actor, e.project = a.ID, p.ID
	return e
}

// newEmptyEnv creates a migrated DB with no rows (a fresh clone's cache).
func newEmptyEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "lore.db")
	if err := dbtemplate.Copy(templateDB, path); err != nil {
		t.Fatal(err)
	}
	db := dbent.InitDB(path)
	if err := dbent.ApplyPragmas(db); err != nil {
		t.Fatal(err)
	}
	client := dbent.New(db).Client()
	t.Cleanup(func() { _ = client.Close() })
	return &env{t: t, dir: dir, data: filepath.Join(dir, "data"), db: db, client: client}
}

func (e *env) opts(mod ...func(*Options)) Options {
	o := Options{DB: e.db, DataDir: e.data, LockPath: filepath.Join(e.dir, "sync.lock")}
	for _, m := range mod {
		m(&o)
	}
	return o
}

func (e *env) reconcile(mod ...func(*Options)) Report {
	e.t.Helper()
	rep, err := Reconcile(context.Background(), e.opts(mod...))
	if err != nil {
		e.t.Fatalf("reconcile: %v", err)
	}
	return rep
}

func (e *env) addMemory(body string) string {
	e.t.Helper()
	m, err := e.client.Memory.Create().SetProjectID(e.project).SetBody(body).
		SetSourceKind("manual").SetCreatedByActorID(e.actor).Save(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return m.ID
}

func (e *env) memoryBody(id string) string {
	e.t.Helper()
	m, err := e.client.Memory.Get(context.Background(), id)
	if err != nil {
		e.t.Fatalf("get memory %s: %v", id, err)
	}
	return m.Body
}

func (e *env) memoryExists(id string) bool {
	e.t.Helper()
	return e.count(`SELECT COUNT(*) FROM memories WHERE id = ?`, id) == 1
}

func (e *env) file(table, id string) string {
	return filepath.Join(e.data, table, id+".json")
}

func (e *env) readDoc(table, id string) map[string]any {
	e.t.Helper()
	b, err := os.ReadFile(e.file(table, id))
	if err != nil {
		e.t.Fatalf("read %s/%s: %v", table, id, err)
	}
	doc, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if err != nil {
		e.t.Fatalf("decode %s/%s: %v", table, id, err)
	}
	return doc
}

// editFile rewrites a row file the way a teammate's commit would. writeRaw
// moves the mtime away from the recorded one so the stat cache sees it.
func (e *env) editFile(table, id string, mutate func(map[string]any)) {
	e.t.Helper()
	doc := e.readDoc(table, id)
	mutate(doc)
	e.writeDoc(table, id, doc)
}

func (e *env) writeDoc(table, id string, doc map[string]any) {
	e.t.Helper()
	b, err := canonjson.Encode(doc)
	if err != nil {
		e.t.Fatal(err)
	}
	e.writeRaw(table, id, b)
}

func (e *env) writeRaw(table, id string, b []byte) {
	e.t.Helper()
	p := e.file(table, id)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		e.t.Fatal(err)
	}
	future := time.Now().Add(time.Duration(len(b)%7+1) * time.Hour)
	if err := os.Chtimes(p, future, future); err != nil {
		e.t.Fatal(err)
	}
}

// copyDataTo copies this clone's data dir into another clone (a git pull).
func (e *env) copyDataTo(dst *env) {
	e.t.Helper()
	if err := os.RemoveAll(dst.data); err != nil {
		e.t.Fatal(err)
	}
	err := filepath.WalkDir(e.data, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(e.data, p)
		target := filepath.Join(dst.data, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func hasPrefixIn(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func num(n int) json.Number { return json.Number(strconv.Itoa(n)) }
