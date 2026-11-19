package engine

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// writeCount is a memstore backend that counts its writes (one backend
// write = one commit in a git store) and keeps each write's trailers.
type writeCount struct {
	*memstore.Backend
	mu       sync.Mutex
	writes   int
	trailers [][]string
}

func (c *writeCount) Write(ctx context.Context, changes []store.Change) error {
	c.mu.Lock()
	c.writes++
	c.trailers = append(c.trailers, store.CommitTrailers(ctx))
	c.mu.Unlock()
	return c.Backend.Write(ctx, changes)
}

func (c *writeCount) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes
}

// bulkWorld: a source store (src), an own target (dst) and a client repo,
// the two own stores behind write counters.
type bulkWorld struct {
	eng      *Engine
	src, dst *writeCount
	srcData  *memstore.Data
	dstData  *memstore.Data
	prj      ids.AnyID
	repoPrj  ids.AnyID
}

const (
	bulkSrc store.Source = "notes/old"
	bulkDst store.Source = "notes/new"
)

func newBulkWorld(t *testing.T) *bulkWorld {
	t.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scan.NewRegexScanner([]scan.Rule{{ID: awsRule, Regex: `AKIA[0-9A-Z]{16}`, Keywords: []string{"akia"}, Source: scan.SourceBuiltin}})
	if err != nil {
		t.Fatal(err)
	}
	w := &bulkWorld{srcData: memstore.NewData(), dstData: memstore.NewData()}
	w.src = &writeCount{Backend: w.srcData.Open(store.OpenOptions{})}
	w.dst = &writeCount{Backend: w.dstData.Open(store.OpenOptions{})}
	if w.eng, err = New(WithCache(newFakeCache()), WithRegistry(reg), WithScanner(sc), WithActor(testActor), WithAudit(&auditLog{}),
		WithStore(Attached{Name: store.SourceRepo, Backend: memstore.NewData().Open(store.OpenOptions{}), Client: true}),
		WithStore(Attached{Name: bulkSrc, Backend: w.src}),
		WithStore(Attached{Name: bulkDst, Backend: w.dst}),
	); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := w.eng.Create(ctx, bulkSrc, schema.TableProjects, map[string]value.Value{"name": txt("old")})
	if err != nil {
		t.Fatal(err)
	}
	w.prj = p.Row.ID
	rp, err := w.eng.Create(ctx, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": txt("client")})
	if err != nil {
		t.Fatal(err)
	}
	w.repoPrj = rp.Row.ID
	return w
}

func (w *bulkWorld) mem(t *testing.T, body string) Ref {
	t.Helper()
	got, err := w.eng.Create(context.Background(), bulkSrc, schema.TableMemories, map[string]value.Value{
		"project_id": txt(w.prj.String()), "kind": txt(schema.MemoryKindCore), "body": txt(body), "source_kind": txt("manual"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return Ref{Store: bulkSrc, Table: schema.TableMemories, ID: got.Row.ID}
}

func (w *bulkWorld) task(t *testing.T, title string, parent ids.AnyID) Ref {
	t.Helper()
	fields := map[string]value.Value{
		"project_id": mustRef(t, w.prj), "title": txt(title), "status": txt(schema.TaskStatusTodo),
		"priority": txt(schema.TaskPriorityMedium), "commitment": txt(schema.TaskCommitmentAccepted),
	}
	if !parent.IsZero() {
		fields[schema.FieldTaskParent] = mustRef(t, parent)
	}
	got, err := w.eng.Create(context.Background(), bulkSrc, schema.TableTasks, fields)
	if err != nil {
		t.Fatal(err)
	}
	return Ref{Store: bulkSrc, Table: schema.TableTasks, ID: got.Row.ID}
}

// liveIn reports whether ref's row is live in store n.
func (w *bulkWorld) liveIn(t *testing.T, n store.Source, ref Ref) bool {
	t.Helper()
	sr, _, err := w.eng.getRow(context.Background(), n, ref.Table, ref.ID)
	return err == nil && !sr.Deleted
}

// TestMoveManyOneWritePerStore (devco D-712): a bulk move of many rows is
// ONE write at the target and ONE at the source (it was two per row), every
// row keeps its id and created time, gets a newer HLC and a moves entry, the
// source write carries Lore-Moved-To, and a sub-task moved with its parent
// (listed child first) gets no "moved from parent" note.
func TestMoveManyOneWritePerStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newBulkWorld(t)
	parent := w.task(t, "parent", ids.AnyID{})
	child := w.task(t, "child", parent.ID)
	refs := []Ref{child, parent} // child first: MoveMany orders parents first
	for i := range 6 {
		refs = append(refs, w.mem(t, "note "+string(rune('a'+i))))
	}
	before := map[ids.AnyID][2]string{}
	for _, r := range refs {
		_, cur, err := w.eng.locate(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		before[r.ID] = [2]string{cur.CreatedAt.String(), cur.HLC.String()}
	}
	s0, d0 := w.src.count(), w.dst.count()
	got, err := w.eng.MoveMany(ctx, refs, bulkDst, MoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n := w.dst.count() - d0; n != 1 {
		t.Fatalf("target writes = %d, want 1 for %d rows", n, len(refs))
	}
	if n := w.src.count() - s0; n != 1 {
		t.Fatalf("source writes = %d, want 1 for %d rows", n, len(refs))
	}
	if tr := strings.Join(w.src.trailers[len(w.src.trailers)-1], "\n"); !strings.Contains(tr, MovedToTrailer+": "+string(bulkDst)) {
		t.Fatalf("source commit trailers = %q, want %s", tr, MovedToTrailer)
	}
	if len(got.Rows) != len(refs) || got.Rows[0].Row.ID != parent.ID {
		t.Fatalf("moved %d rows, first %s; want %d, parent first", len(got.Rows), got.Rows[0].Row.ID, len(refs))
	}
	for _, r := range refs {
		if !w.liveIn(t, bulkDst, r) || w.liveIn(t, bulkSrc, r) {
			t.Fatalf("%s: live in target %v, in source %v", r.ID, w.liveIn(t, bulkDst, r), w.liveIn(t, bulkSrc, r))
		}
		_, now, err := w.eng.locate(ctx, Ref{Store: bulkDst, Table: r.Table, ID: r.ID})
		if err != nil {
			t.Fatal(err)
		}
		if now.CreatedAt.String() != before[r.ID][0] || now.HLC.String() <= before[r.ID][1] || !strings.Contains(now.Origin.String(), `"moved_to"`) {
			t.Fatalf("%s: created %s (was %s), hlc %s (was %s), _o %s", r.ID, now.CreatedAt, before[r.ID][0], now.HLC, before[r.ID][1], now.Origin.String())
		}
	}
	rows, _, err := w.dst.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, sr := range rows {
		if sr.Table == schema.TableComments && !sr.Deleted {
			t.Fatalf("a sub-task moved with its parent got a note: %s", sr.ID)
		}
	}
}

// TestMoveManyRefusedRowWritesNothing (D-712): one row the client repo
// refuses (a secret reference) refuses the whole batch before any write:
// no write in either store, every row still live where it was.
func TestMoveManyRefusedRowWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newBulkWorld(t)
	refs := []Ref{w.mem(t, "plain one"), w.mem(t, "ships with ${secret:deploy_key}"), w.mem(t, "plain two")}
	s0 := w.src.count()
	_, err := w.eng.MoveMany(ctx, refs, store.SourceRepo, MoveOptions{})
	if !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "nothing moved") {
		t.Fatalf("bulk move with a secret ref into a client repo: %v", err)
	}
	if n := w.src.count() - s0; n != 0 {
		t.Fatalf("source writes after a refusal = %d, want 0", n)
	}
	for _, r := range refs {
		if !w.liveIn(t, bulkSrc, r) || w.liveIn(t, store.SourceRepo, r) {
			t.Fatalf("%s moved although the batch was refused", r.ID)
		}
	}
}

// TestMoveManyLiveCopy (D-712): a row already live in the target too (an
// adopt that kept the source, an interrupted move) gets only its source
// tombstone, in the same one source write; the other rows move as usual.
func TestMoveManyLiveCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newBulkWorld(t)
	twin := w.mem(t, "already in both")
	raw, err := w.src.Get(ctx, twin.Table, twin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.dstData.Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: raw}}); err != nil {
		t.Fatal(err)
	}
	other := w.mem(t, "only in the source")
	s0, d0 := w.src.count(), w.dst.count()
	if _, err = w.eng.MoveMany(ctx, []Ref{twin, other}, bulkDst, MoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if ds, ss := w.dst.count()-d0, w.src.count()-s0; ds != 1 || ss != 1 {
		t.Fatalf("writes target %d source %d, want 1 and 1", ds, ss)
	}
	for _, r := range []Ref{twin, other} {
		if !w.liveIn(t, bulkDst, r) || w.liveIn(t, bulkSrc, r) {
			t.Fatalf("%s: live in target %v, in source %v", r.ID, w.liveIn(t, bulkDst, r), w.liveIn(t, bulkSrc, r))
		}
	}
	if _, _, err = w.eng.locateExcept(ctx, Ref{ID: twin.ID}, store.SourceRepo); err != nil {
		t.Fatalf("after the move the id is ambiguous or lost: %v", err)
	}
}
