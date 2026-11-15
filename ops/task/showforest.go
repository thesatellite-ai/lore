package task

// showforest.go — the part of the task tree one task show needs (devco
// T-20 follow-up: task show at ~5,000 open tasks took 4-5 s).
//
// task show built the forest of every task of the project (every task row
// read, converted, decoded and scanned) to answer four things about one
// task: its parent, its descendants (rollup, --tree lines), and a parent
// cycle through it. Those need only the task's ancestor chain (its parent
// field, followed) and its descendants, found through the cache's tasks
// table (children by parent_id) and each read back through the engine,
// which stays the authority: a child whose row does not name the parent
// is not taken. Without the cache's SQL (another Env), or past
// showForestMax rows, the whole forest is built as before.

import (
	"context"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	"github.com/thesatellite-ai/lore/schema"
)

// showForestMax bounds the rows of a small forest; a bigger tree is built
// whole (one listing is then cheaper than a lookup per row).
const showForestMax = 500

// lensAllSuffix names the cache lens's view of a table over every attached
// store ("tasks_all"; adapters/cache/sqlite OpenLens).
const lensAllSuffix = "_all"

// showForestTimeout bounds each children query.
const showForestTimeout = 5 * time.Second

// cacheSQL is the Env capability showForest uses (wire.App: the cache's
// read-only SQL lens, as lore sql runs it).
type cacheSQL interface {
	SQL(ctx context.Context, r opssql.Request) (opssql.Result, error)
}

// showForest is the forest of r's ancestors, r and r's descendants in
// project; ok is false when it cannot be built that way (the caller builds
// the whole forest).
func (o ops) showForest(ctx context.Context, r row.Row, project string) (forest, bool, error) {
	q, ok := o.env.(cacheSQL)
	if !ok {
		return forest{}, false, nil
	}
	f := forest{rows: map[string]work.Sourced{}}
	var nodes []tasktree.Node
	add := func(s work.Sourced) bool {
		id := s.Row.ID.String()
		if _, dup := f.rows[id]; dup || s.Row.Deleted || work.Field(s.Row, work.FieldProject) != project {
			return false
		}
		f.rows[id] = s
		nodes = append(nodes, nodeOf(s.Row))
		return true
	}
	src, me, err := work.Get(ctx, o.env, Noun, table, r.ID)
	if err != nil {
		return forest{}, false, err
	}
	if !add(work.Sourced{Source: src, Row: me}) {
		return forest{}, false, nil
	}
	// ancestors: the parent field followed (a cycle stops at a row seen)
	for at := me; len(f.rows) < showForestMax; {
		pid := work.Field(at, fieldParent)
		if pid == "" {
			break
		}
		id, perr := ids.ParseAny(pid)
		if perr != nil {
			break
		}
		psrc, prow, gerr := work.Get(ctx, o.env, Noun, table, id)
		if gerr != nil || !add(work.Sourced{Source: psrc, Row: prow}) {
			break // missing, trashed, another project or seen: the whole forest says the same
		}
		at = prow
	}
	// descendants: children by parent_id, level by level
	frontier := []string{me.ID.String()}
	for len(frontier) > 0 {
		kids, kerr := childIDs(ctx, q, frontier)
		if kerr != nil {
			return forest{}, false, nil // the cache cannot answer: the whole forest
		}
		parents := map[string]bool{}
		for _, p := range frontier {
			parents[p] = true
		}
		frontier = nil
		for _, k := range kids {
			if len(f.rows) >= showForestMax {
				return forest{}, false, nil
			}
			id, perr := ids.ParseAny(k)
			if perr != nil {
				continue
			}
			csrc, crow, gerr := work.Get(ctx, o.env, Noun, table, id)
			if gerr != nil || !parents[work.Field(crow, fieldParent)] {
				continue // the engine's row is the authority
			}
			if add(work.Sourced{Source: csrc, Row: crow}) {
				frontier = append(frontier, k)
			}
		}
	}
	f.tree = tasktree.Build(nodes)
	return f, true, nil
}

// childIDs lists the ids of the tasks whose parent_id is one of parents,
// from the cache. parents are task ids (checked), so they are quoted as
// they are.
func childIDs(ctx context.Context, q cacheSQL, parents []string) ([]string, error) {
	quoted := make([]string, 0, len(parents))
	for _, p := range parents {
		if _, err := taskID(p); err != nil || strings.ContainsAny(p, `'"\`) {
			continue
		}
		quoted = append(quoted, "'"+p+"'")
	}
	if len(quoted) == 0 {
		return nil, nil
	}
	// devco:D-388-tree: the union view over every attached store, and the
	// internal stores of a client repo too: an unqualified tasks table is
	// the repo's own, so a tree in a store showed no children. Only ids
	// come back, each read through the engine, which decides what shows.
	res, err := q.SQL(ctx, opssql.Request{Query: "SELECT id FROM " + string(schema.TableTasks) + lensAllSuffix + " WHERE " + schema.FieldTaskParent + " IN (" + strings.Join(quoted, ",") + ")", Timeout: showForestTimeout, Internal: true})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) > 0 && r[0] != nil {
			out = append(out, r[0].String())
		}
	}
	return out, nil
}

// showTreeOf is the forest task show reads r's tree from: showForest, or
// the whole project's forest when that cannot be built.
// It is built once per show (showTreeMemo on ctx).
func (o ops) showTreeOf(ctx context.Context, r row.Row) (forest, error) {
	m, _ := ctx.Value(showTreeKey{}).(*showTreeMemo)
	if m != nil && m.built && m.id == r.ID.String() {
		return m.f, nil
	}
	project := work.Field(r, work.FieldProject)
	f, small, err := o.showForest(ctx, r, project)
	if err == nil && !small {
		f, err = o.forest(ctx, project)
	}
	if err == nil && m != nil {
		m.id, m.f, m.built = r.ID.String(), f, true
	}
	return f, err
}

// showTreeKey is the context key of a show's showTreeMemo.
type showTreeKey struct{}

// showTreeMemo holds the forest one task show built (its tree, its subtree
// estimate).
type showTreeMemo struct {
	id    string
	f     forest
	built bool
}

// withShowTree returns ctx holding an empty showTreeMemo.
func withShowTree(ctx context.Context) context.Context {
	return context.WithValue(ctx, showTreeKey{}, &showTreeMemo{})
}
