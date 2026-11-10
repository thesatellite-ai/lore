package run

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/localstate"
)

// fakeEnv is an in-memory Env: rows by id, a fixed clock, an actor, a
// project and a local state; unreachable makes every task lookup answer
// E_STORE_UNREACHABLE (the TIME 4.3 outbox path).
type fakeEnv struct {
	rows        map[string]row.Row
	now         time.Time
	actor       ids.ID[ids.Actor]
	local       *fakeLocal
	unreachable bool
}

func newFake(t *testing.T) *fakeEnv {
	t.Helper()
	a, err := ids.Parse[ids.Actor]("act_0e45506e64357c94a87e70ef1c6bbba9")
	if err != nil {
		t.Fatal(err)
	}
	return &fakeEnv{rows: map[string]row.Row{}, now: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC), actor: a, local: &fakeLocal{docs: map[string][]byte{}}}
}

func (f *fakeEnv) Create(_ context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error) {
	if target != store.SourceMachine {
		return engine.Written{}, errs.New(errs.InvalidInput, "runs go to the machine store")
	}
	id, err := newID(ids.PrefixRun)
	if err != nil {
		return engine.Written{}, err
	}
	r := row.Row{Table: table, ID: id, Fields: fields}
	f.rows[id.String()] = r
	return engine.Written{Row: r}, nil
}

func (f *fakeEnv) Update(_ context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error) {
	r := f.rows[ref.ID.String()]
	for k, v := range patch {
		r.Fields[k] = v
	}
	f.rows[ref.ID.String()] = r
	return engine.Written{Row: r}, nil
}

func (f *fakeEnv) Get(_ context.Context, ref engine.Ref) (store.Source, row.Row, error) {
	if ref.Table == schema.TableTasks && f.unreachable {
		return "", row.Row{}, errs.New(errs.StoreUnreachable, "org store unreachable")
	}
	r, ok := f.rows[ref.ID.String()]
	if !ok || r.Table != ref.Table {
		return "", row.Row{}, errs.New(errs.NotFound, "no row")
	}
	return store.SourceMachine, r, nil
}

func (f *fakeEnv) Actor(context.Context) (ids.ID[ids.Actor], error) { return f.actor, nil }
func (f *fakeEnv) Now() time.Time                                   { return f.now }
func (f *fakeEnv) LocalState(context.Context) (localstate.Store, error) {
	return f.local, nil
}

// fakeLocal is a localstate.Store over a map (one table is enough here).
type fakeLocal struct {
	mu   sync.Mutex
	docs map[string][]byte
}

func (l *fakeLocal) Put(_ context.Context, t localstate.Table, key string, doc []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.docs[string(t)+"/"+key] = doc
	return nil
}

func (l *fakeLocal) Get(_ context.Context, t localstate.Table, key string) (localstate.Entry, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d, ok := l.docs[string(t)+"/"+key]
	return localstate.Entry{Key: key, Doc: d}, ok, nil
}

func (l *fakeLocal) List(_ context.Context, t localstate.Table, prefix string) ([]localstate.Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []localstate.Entry
	for k, d := range l.docs {
		if key, ok := strings.CutPrefix(k, string(t)+"/"); ok && strings.HasPrefix(key, prefix) {
			out = append(out, localstate.Entry{Key: key, Doc: d})
		}
	}
	return out, nil
}

func (l *fakeLocal) Delete(_ context.Context, t localstate.Table, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.docs, string(t)+"/"+key)
	return nil
}

func (l *fakeLocal) Close() error { return nil }

// addTask seeds a task row and returns its id.
func (f *fakeEnv) addTask(t *testing.T) string {
	t.Helper()
	id, err := newID(ids.PrefixTask)
	if err != nil {
		t.Fatal(err)
	}
	f.rows[id.String()] = row.Row{Table: schema.TableTasks, ID: id, Fields: map[string]value.Value{}}
	return id.String()
}

func TestStartEndWritesSpanOnTask(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	o := ops{env: f}
	task := f.addTask(t)
	st, err := o.start(ctx, StartIn{Task: task, Goal: "add rate limit"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != kindTask || st.Text() != st.ID+" started\n" {
		t.Fatalf("start: %+v %q", st, st.Text())
	}
	f.now = f.now.Add(45*time.Minute + 10*time.Second)
	end, err := o.end(ctx, EndIn{ID: st.ID, Outcome: schema.RunSuccess})
	if err != nil {
		t.Fatal(err)
	}
	if end.Text() != st.ID+" ended  (45m 10s)\n" || end.Seconds != 2710 || end.TimeEntry == "" {
		t.Fatalf("end: %+v %q", end, end.Text())
	}
	entries := opstask.TimeEntries(f.rows[task])
	if len(entries) != 1 || entries[0].LocalRef != st.ID {
		t.Fatalf("task time: %+v", entries)
	}
	if got := fieldText(f.rows[st.ID], schema.FieldRunStatus); got != schema.RunSuccess {
		t.Fatalf("status %q", got)
	}
}

// TestEndWithoutStartTime: a run whose started_at is unset (a v0.1.17
// lore.db copy, found migrating a real repo: the old run commands
// dereferenced it and crashed) ends, says "start time unknown", has no
// length and writes no span on its task (ported from the run half of
// TestE2E_ShowCommandsWithOptionalTimesUnset).
func TestEndWithoutStartTime(t *testing.T) {
	ctx := context.Background()
	for _, cancel := range []bool{false, true} {
		f := newFake(t)
		o := ops{env: f}
		task := f.addTask(t)
		st, err := o.start(ctx, StartIn{Task: task})
		if err != nil {
			t.Fatal(err)
		}
		delete(f.rows[st.ID].Fields, schema.FieldRunStartedAt)
		var end EndOut
		if cancel {
			end, err = o.cancel(ctx, CancelIn{ID: st.ID})
		} else {
			end, err = o.end(ctx, EndIn{ID: st.ID, Outcome: schema.RunSuccess})
		}
		if err != nil {
			t.Fatal(err)
		}
		if end.Elapsed != startUnknown || end.Seconds != 0 || end.TimeEntry != "" || end.TaskID != "" {
			t.Fatalf("cancel=%v: %+v", cancel, end)
		}
		if !cancel && !strings.Contains(end.Text(), startUnknown) {
			t.Fatalf("end text: %q", end.Text())
		}
		if n := len(opstask.TimeEntries(f.rows[task])); n != 0 {
			t.Fatalf("cancel=%v: %d span(s) written without a start", cancel, n)
		}
	}
}

func TestEndRefusesUnknownOutcomeAndRun(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	o := ops{env: f}
	st, err := o.start(ctx, StartIn{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != kindGeneral {
		t.Fatalf("kind %q", st.Kind)
	}
	if _, err := o.end(ctx, EndIn{ID: st.ID, Outcome: "great"}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("bad outcome: %v", err)
	}
	for _, id := range []string{"run_00000000000000000000000000000001", "tsk_0e45506e64357c94a87e70ef1c6bbba9", "nope"} {
		if _, err := o.cancel(ctx, CancelIn{ID: id}); !errs.Has(err, errs.InvalidID) { // malformed or another prefix (B-ERRS-1)
			t.Fatalf("%s: %v", id, err)
		}
	}
	if _, err := o.start(ctx, StartIn{Task: "T-7"}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("non-task --task: %v", err)
	}
}

func TestCancelUnreachableTaskQueuesThenFlushes(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	o := ops{env: f}
	task := f.addTask(t)
	st, err := o.start(ctx, StartIn{Task: task})
	if err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Hour)
	f.unreachable = true
	out, err := o.cancel(ctx, CancelIn{ID: st.ID, Reason: "superseded"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Queued || !strings.HasPrefix(out.Text(), st.ID+" cancelled\n! task ") {
		t.Fatalf("cancel: %+v %q", out, out.Text())
	}
	if got := FlushOutbox(ctx, f); got.Written != 0 {
		t.Fatalf("flush while unreachable: %+v", got)
	}
	f.unreachable = false
	if got := FlushOutbox(ctx, f); got.Written != 1 {
		t.Fatalf("flush: %+v", got)
	}
	// Run twice: a replayed span is never added again.
	if got := FlushOutbox(ctx, f); got.Written != 0 {
		t.Fatalf("second flush: %+v", got)
	}
	if n := len(opstask.TimeEntries(f.rows[task])); n != 1 {
		t.Fatalf("entries %d", n)
	}
}

func TestSpaced(t *testing.T) {
	for d, want := range map[time.Duration]string{12 * time.Second: "12s", 45*time.Minute + 10*time.Second: "45m 10s", time.Hour + 2*time.Minute + 3*time.Second: "1h 2m 3s", 0: "0s"} {
		if got := spaced(d); got != want {
			t.Errorf("%v: %q want %q", d, got, want)
		}
	}
}

// newID is a fresh id of prefix p (a natural id over a counter).
var idSeq int

func newID(p ids.Prefix) (ids.AnyID, error) {
	idSeq++
	return ids.Natural(p, "run-test", strconv.Itoa(idSeq))
}
