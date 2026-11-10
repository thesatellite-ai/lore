package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// closureRows reads the closure as sorted "ancestor>descendant@depth" lines
// inside the transaction q.
func closureRows(t *testing.T, ctx context.Context, q *sql.Tx, names map[string]string) []string {
	t.Helper()
	rs, err := q.QueryContext(ctx, `SELECT ancestor_id, descendant_id, depth FROM task_tree`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var out []string
	for rs.Next() {
		var a, d string
		var n int
		if err := rs.Scan(&a, &d, &n); err != nil {
			t.Fatal(err)
		}
		out = append(out, nameOf(names, a)+">"+nameOf(names, d)+"@"+strconv.Itoa(n))
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func nameOf(names map[string]string, id string) string {
	if n, ok := names[id]; ok {
		return n
	}
	return id
}

// assertMatchesFull compares the closure the incremental path left with a
// full rebuild from `_rows` (nodes re-read from every document, as the old
// whole-file refresh did), inside a transaction that is rolled back so the
// incremental state goes on untouched.
func assertMatchesFull(t *testing.T, f *fixture, c *Cache, names map[string]string, step string) {
	t.Helper()
	tx, err := c.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	incr := closureRows(t, f.ctx, tx, names)
	for _, q := range []string{qNodesClear, qNodesBackfill} {
		if _, err := tx.ExecContext(f.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := refreshTaskTree(f.ctx, tx); err != nil {
		t.Fatal(err)
	}
	full := closureRows(t, f.ctx, tx, names)
	if !slices.Equal(incr, full) {
		t.Fatalf("%s: incremental closure differs from a full rebuild\nincremental: %v\nfull:        %v", step, incr, full)
	}
}

// taskRow builds a tasks row under parent with rank; trashed sets
// deleted_at.
func (f *fixture) taskRow(id ids.AnyID, parent, rank string, trashed bool) store.Row {
	f.t.Helper()
	r := f.task(id, parent, trashed)
	if rank == "" {
		return r
	}
	dr := must(r.Decode())
	dr.Fields[schema.FieldTaskRank] = text(f.t, rank)
	return must(store.FromRow(dr))
}

// TestTaskTreeIncrementalMatchesFull: after every write the closure the
// per-write update leaves equals a full rebuild, across add, reparent (a
// whole subtree moving), tombstone, restore, trash and untrash, a parent
// arriving after its children, merged cycles made, re-broken and undone
// (a write that would make a cycle is refused by the engine, E_CYCLE; the
// cache must still read a merged one, 6.13), the same id in two sources, a
// source Rebuild, and a seeded random walk over all of these (P3-PERF-1).
func TestTaskTreeIncrementalMatchesFull(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	const n = 10
	pool := make([]ids.AnyID, n)
	names := map[string]string{}
	for i := range pool {
		pool[i] = newTsk()
		names[pool[i].String()] = string(rune('A' + i))
	}
	A, B, C, D, E, X, Y, Z, P, Q := pool[0], pool[1], pool[2], pool[3], pool[4], pool[5], pool[6], pool[7], pool[8], pool[9]
	cur := 0
	step := func(name string, source store.Source, rows ...store.Row) {
		t.Helper()
		cur++
		f.apply(c, source, strconv.Itoa(cur), rows...)
		assertMatchesFull(t, f, c, names, name)
	}
	s := func(id ids.AnyID) string { return id.String() }

	step("add chain", "repo", f.taskRow(A, "", "b", false), f.taskRow(B, s(A), "", false), f.taskRow(C, s(B), "", false),
		f.taskRow(D, "", "a", false), f.taskRow(E, s(A), "", false))
	step("reparent subtree", "repo", f.taskRow(B, s(D), "", false))
	step("tombstone parent", "repo", f.tomb(schema.TableTasks, D))
	step("restore parent", "repo", f.taskRow(D, "", "", false))
	step("trash middle", "repo", f.taskRow(B, s(D), "", true))
	step("untrash middle", "repo", f.taskRow(B, s(D), "", false))
	step("rank only", "repo", f.taskRow(C, s(B), "z", false))
	step("child before parent", "repo", f.taskRow(Z, s(Y), "", false))
	step("parent arrives", "repo", f.taskRow(Y, "", "", false))
	step("cycle in one write", "repo", f.taskRow(X, s(Y), "", false), f.taskRow(Y, s(X), "", false))
	step("cycle broken by a move", "repo", f.taskRow(X, "", "", false))
	// a missing parent arriving closes a cycle: Q waits under P, P lands under Q's child
	step("waiting child", "repo", f.taskRow(Q, s(P), "", false), f.taskRow(E, s(Q), "", false))
	step("arrival closes a cycle", "repo", f.taskRow(P, s(E), "", false))
	step("cycle member deleted", "repo", f.tomb(schema.TableTasks, Q))
	step("cycle member back", "repo", f.taskRow(Q, s(P), "", false))
	step("cycle member trashed", "repo", f.taskRow(P, s(E), "", true))
	step("same id in a second source", "org", f.taskRow(B, s(A), "", false), f.taskRow(A, "", "", false))
	step("second source moves its copy", "org", f.taskRow(C, "", "", false))
	if err := c.Rebuild(f.ctx, "repo"); err != nil {
		t.Fatal(err)
	}
	assertMatchesFull(t, f, c, names, "rebuild repo")
	step("after rebuild", "repo", f.taskRow(D, s(B), "", false))

	// seeded random walk: 1-3 rows per write over the pool, parents drawn
	// from the pool, top level or a missing id
	rng := &walk{state: 18}
	missing := newTsk().String()
	sources := []store.Source{"repo", "org"}
	for i := range 400 {
		k := 1 + rng.IntN(3)
		rows := make([]store.Row, 0, k)
		seen := map[int]bool{}
		for range k {
			j := rng.IntN(n)
			if seen[j] {
				continue
			}
			seen[j] = true
			switch rng.IntN(10) {
			case 0:
				rows = append(rows, f.tomb(schema.TableTasks, pool[j]))
			case 1:
				rows = append(rows, f.taskRow(pool[j], "", "", true))
			default:
				parent := ""
				switch p := rng.IntN(n + 2); {
				case p < n:
					parent = s(pool[p])
				case p == n:
					parent = missing
				}
				rows = append(rows, f.taskRow(pool[j], parent, strconv.Itoa(rng.IntN(3)), false))
			}
		}
		src := sources[rng.IntN(len(sources))]
		if rng.IntN(50) == 0 {
			if err := c.Rebuild(f.ctx, src); err != nil {
				t.Fatal(err)
			}
			assertMatchesFull(t, f, c, names, "random rebuild "+strconv.Itoa(i))
			continue
		}
		var desc []string
		for _, r := range rows {
			desc = append(desc, names[r.ID.String()])
		}
		step("random "+strconv.Itoa(i)+" "+string(src)+" "+strings.Join(desc, ","), src, rows...)
	}
}

// TestTaskTreeReadsNodesNotDocs: a task write reads the tree from
// `_task_nodes`, so a node written there directly (no document behind it)
// shows in the closure of the next write: the guard that the per-write path
// does not go back to the task documents (P3-PERF-1).
func TestTaskTreeReadsNodesNotDocs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	a, b := newTsk(), newTsk()
	f.apply(c, "repo", "1", f.taskRow(a, "", "", false))
	if _, err := c.db.ExecContext(f.ctx, `INSERT INTO "`+tblTaskNodes+`" (source, id, parent, rank) VALUES ('repo', ?, ?, '')`, b.String(), a.String()); err != nil {
		t.Fatal(err)
	}
	f.apply(c, "repo", "2", f.taskRow(a, "", "x", false))
	tx, err := c.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if got := closureRows(t, f.ctx, tx, nil); !slices.Contains(got, a.String()+">"+b.String()+"@1") {
		t.Fatalf("closure = %v", got)
	}
}

// walk is a fixed-seed xorshift sequence: the random walk is the same on
// every run, so a failing step is reproducible by name.
type walk struct{ state uint64 }

// IntN returns the next value in [0, n) for n > 0 (drawn from the low 31
// bits of the state, which always fit an int).
func (w *walk) IntN(n int) int {
	w.state ^= w.state << 13
	w.state ^= w.state >> 7
	w.state ^= w.state << 17
	v := 0
	for b := range 31 {
		if w.state&(1<<b) != 0 {
			v |= 1 << b
		}
	}
	return v % n
}
