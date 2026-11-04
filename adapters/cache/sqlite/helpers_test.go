package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// refPattern is the fake reference syntax: ${ref:<id>}.
var refPattern = regexp.MustCompile(`\$\{ref:([a-z]+_[0-9a-f]+)\}`)

// fakeExpander resolves ${ref:<id>} from a map, records every text it was
// asked to expand, and fails on texts containing failOn (when set).
type fakeExpander struct {
	mu     sync.Mutex
	vals   map[string]string
	calls  []string
	failOn string
}

func newExpander() *fakeExpander { return &fakeExpander{vals: map[string]string{}} }

func (f *fakeExpander) Expand(text string) (string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, text)
	if f.failOn != "" && strings.Contains(text, f.failOn) {
		return "", nil, errors.New("fake expander: injected failure")
	}
	var refs []string
	out := refPattern.ReplaceAllStringFunc(text, func(m string) string {
		id := refPattern.FindStringSubmatch(m)[1]
		refs = append(refs, id)
		if v, ok := f.vals[id]; ok {
			return v
		}
		return m
	})
	return out, refs, nil
}

func (f *fakeExpander) set(id, v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vals[id] = v
}

func (f *fakeExpander) resetCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls
	f.calls = nil
	return c
}

// fixture is one test's cache world.
type fixture struct {
	t     *testing.T
	ctx   context.Context
	reg   *schema.Registry
	exp   *fakeExpander
	clock *hlc.Clock
	actor ids.ID[ids.Actor]
	dir   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	actor := must(ids.New[ids.Actor]())
	return &fixture{
		t: t, ctx: context.Background(), reg: reg, exp: newExpander(),
		clock: hlc.NewClock(actor, time.Now), actor: actor, dir: t.TempDir(),
	}
}

// open opens (or reopens) the cache file name in the fixture dir.
func (f *fixture) open(name string) *Cache {
	f.t.Helper()
	c, err := Open(f.ctx, filepath.Join(f.dir, name), f.reg, f.exp)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = c.Close() })
	return c
}

// must unwraps a (value, error) pair in test setup; an error panics, which
// fails the running test with the stack of the call that broke.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func text(t *testing.T, s string) value.Text {
	t.Helper()
	return must(value.TextOf(s))
}

// live builds a stored row of table with fields, stamped now.
func (f *fixture) live(table row.Table, id ids.AnyID, fields map[string]value.Value) store.Row {
	f.t.Helper()
	stamp := f.clock.Now()
	return must(store.FromRow(row.Row{
		Table: table, ID: id, HLC: stamp, CreatedAt: stamp.Wall, CreatedBy: f.actor,
		UpdatedAt: stamp.Wall, UpdatedBy: f.actor, Fields: fields,
	}))
}

// tomb builds the tombstone of id, stamped now.
func (f *fixture) tomb(table row.Table, id ids.AnyID) store.Row {
	f.t.Helper()
	stamp := f.clock.Now()
	return must(store.FromRow(row.Row{Table: table, ID: id, HLC: stamp, Deleted: true}))
}

// memory builds a memories row with body.
func (f *fixture) memory(id ids.AnyID, project ids.AnyID, body string) store.Row {
	f.t.Helper()
	return f.live(schema.TableMemories, id, map[string]value.Value{
		"project_id":  must(value.RefOf(project)),
		"kind":        text(f.t, schema.MemoryKindCore),
		"body":        text(f.t, body),
		"source_kind": text(f.t, "test"),
	})
}

func (f *fixture) apply(c *Cache, source store.Source, cursor string, rows ...store.Row) {
	f.t.Helper()
	if err := c.Apply(f.ctx, source, rows, store.Cursor(cursor)); err != nil {
		f.t.Fatal(err)
	}
}

func newMem() ids.AnyID { return must(ids.New[ids.Memory]()).Any() }
func newPrj() ids.AnyID { return must(ids.New[ids.Project]()).Any() }

// count runs a COUNT(*) style query on the cache's own connection.
func count(t *testing.T, c *Cache, q string, args ...string) int {
	t.Helper()
	qa := make([]any, len(args))
	for i, a := range args {
		qa[i] = a
	}
	var n int
	if err := c.db.QueryRowContext(context.Background(), q, qa...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// strings1 reads a one-column string result.
func strings1(t *testing.T, c *Cache, q string, arg string) []string {
	t.Helper()
	rs, err := c.db.QueryContext(context.Background(), q, arg)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer func() { _ = rs.Close() }()
	var out []string
	for rs.Next() {
		var s string
		if err := rs.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// columnsOf lists a table's columns in the cache file.
func columnsOf(t *testing.T, c *Cache, table string) []string {
	t.Helper()
	rs, err := c.db.QueryContext(context.Background(), "SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var out []string
	for rs.Next() {
		var s string
		if err := rs.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func mkdirAll(dir string) error { return os.MkdirAll(dir, 0o750) }

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
