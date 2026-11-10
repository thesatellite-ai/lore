package coord

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/store"
)

// fakeReassignHost is a Host whose only real verbs are the reassign pair:
// every other method is never called by these tests (the embedded nil Host
// would panic if one were).
type fakeReassignHost struct {
	Host
	plan    ReassignPlan
	failOn  string // task id whose reassign fails
	called  []string
	batches []engine.Batch
}

func (h *fakeReassignHost) TaskReassignPlan(_ context.Context, _ ReassignIn) (ReassignPlan, error) {
	return h.plan, nil
}

func (h *fakeReassignHost) TaskReassign(ctx context.Context, in ReassignIn) (Report, error) {
	h.called = append(h.called, in.ID)
	b, _ := engine.BatchOf(ctx)
	h.batches = append(h.batches, b)
	if in.ID == h.failOn {
		return Report{}, errs.New(errs.LostClaim, "lost")
	}
	return Report{Lines: []string{"✓ " + in.ID + " reassigned to " + in.Assign}}, nil
}

// fakeCoordEnv is an Env with the host above and, when rec is set, the
// batch log upgrade.
type fakeCoordEnv struct{ h Host }

func (e fakeCoordEnv) Coordinator(context.Context) (Host, bool) { return e.h, true }

type recordingEnv struct {
	fakeCoordEnv
	events []engine.Event
}

func (e *recordingEnv) RecordBatchEvent(_ context.Context, ev engine.Event) error {
	e.events = append(e.events, ev)
	return nil
}

func taskIDs(t *testing.T, n int) []ReassignTask {
	t.Helper()
	out := make([]ReassignTask, n)
	for i := range out {
		id, err := ids.Natural(ids.PrefixTask, t.Name(), strconv.Itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		out[i] = ReassignTask{ID: id.String(), Title: "t" + strconv.Itoa(i)}
	}
	return out
}

func newActor(t *testing.T) string {
	t.Helper()
	a, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	return a.String()
}

// TestReassignFromIsOneBatch (TASK 18.1, TSQ6, XQ1): --from reassigns each
// open task by id under one bat_ id, records one batch log event per task
// with that id, kind task, the op, the repo store and the acting person,
// and prints the batch line.
func TestReassignFromIsOneBatch(t *testing.T) {
	t.Parallel()
	tasks := taskIDs(t, 3)
	actor := newActor(t)
	h := &fakeReassignHost{plan: ReassignPlan{Tasks: tasks, Store: store.SourceRepo, Actor: actor}}
	env := &recordingEnv{fakeCoordEnv: fakeCoordEnv{h: h}}
	out, err := ops{env: env}.reassign(context.Background(), ReassignIn{From: "@bob", Assign: "@alice"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Batch == "" || !strings.HasPrefix(out.Batch, "bat_") {
		t.Fatalf("batch %q", out.Batch)
	}
	if len(h.called) != 3 || len(out.Tasks) != 3 || len(env.events) != 3 {
		t.Fatalf("called %v tasks %v events %d", h.called, out.Tasks, len(env.events))
	}
	for i, ev := range env.events {
		if ev.Batch.ID.String() != out.Batch || ev.Batch.Kind != string(bulk.KindTask) || h.batches[i].ID.String() != out.Batch {
			t.Fatalf("event %d batch %+v host ctx %+v want %s", i, ev.Batch, h.batches[i], out.Batch)
		}
		if ev.Op != OpTaskReassign || ev.Action != engine.ActionUpdate || ev.Store != store.SourceRepo ||
			ev.ID.String() != tasks[i].ID || ev.Actor.String() != actor || ev.HLC.Wall.IsZero() {
			t.Fatalf("event %d = %+v", i, ev)
		}
	}
	last := out.Lines[len(out.Lines)-1]
	if !strings.Contains(last, "3 tasks reassigned to @alice (batch "+out.Batch+")") {
		t.Fatalf("last line %q", last)
	}
}

// TestReassignFromPreview (XQ2): above batch.preview_rows (default 20) an
// unconfirmed --from is E_NEEDS_CONFIRM listing the rows, and nothing is
// reassigned.
func TestReassignFromPreview(t *testing.T) {
	t.Parallel()
	h := &fakeReassignHost{plan: ReassignPlan{Tasks: taskIDs(t, bulk.DefaultPreviewRows+1), Store: store.SourceRepo, Actor: newActor(t)}}
	env := &recordingEnv{fakeCoordEnv: fakeCoordEnv{h: h}}
	_, err := ops{env: env}.reassign(context.Background(), ReassignIn{From: "@bob", Assign: "@alice"})
	if errs.CodeOf(err) != errs.NeedsConfirm || !strings.Contains(err.Error(), "task reassign --from would change 21 rows") {
		t.Fatalf("err %v", err)
	}
	if len(h.called) != 0 || len(env.events) != 0 {
		t.Fatalf("wrote: called %v events %d", h.called, len(env.events))
	}
}

// TestReassignFromPartialFailure: a failing task stops the batch with its
// own code and names the batch; the tasks before it stay recorded.
func TestReassignFromPartialFailure(t *testing.T) {
	t.Parallel()
	tasks := taskIDs(t, 3)
	h := &fakeReassignHost{plan: ReassignPlan{Tasks: tasks, Store: store.SourceRepo, Actor: newActor(t)}, failOn: tasks[1].ID}
	env := &recordingEnv{fakeCoordEnv: fakeCoordEnv{h: h}}
	out, err := ops{env: env}.reassign(context.Background(), ReassignIn{From: "@bob", Assign: "@alice"})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.LostClaim || !strings.Contains(err.Error(), "batch "+out.Batch) ||
		!strings.Contains(err.Error(), "after 1 earlier tasks") {
		t.Fatalf("err %v", err)
	}
	if len(env.events) != 1 || len(h.called) != 2 {
		t.Fatalf("events %d called %v", len(env.events), h.called)
	}
}

// TestReassignShapes: one id takes the single path (no batch, no plan);
// nothing open for --from writes nothing; a surface without a batch log
// still reassigns under one id and says the batch is not listed.
func TestReassignShapes(t *testing.T) {
	t.Parallel()
	tasks := taskIDs(t, 2)
	t.Run("one id", func(t *testing.T) {
		t.Parallel()
		h := &fakeReassignHost{plan: ReassignPlan{Tasks: tasks}}
		out, err := ops{env: fakeCoordEnv{h: h}}.reassign(context.Background(), ReassignIn{ID: tasks[0].ID, Assign: "@alice"})
		if err != nil || out.Batch != "" || len(out.Tasks) != 1 || len(h.called) != 1 || !h.batches[0].IsZero() {
			t.Fatalf("out %+v err %v called %v", out, err, h.called)
		}
	})
	t.Run("nothing open", func(t *testing.T) {
		t.Parallel()
		h := &fakeReassignHost{plan: ReassignPlan{Tasks: []ReassignTask{}}}
		out, err := ops{env: fakeCoordEnv{h: h}}.reassign(context.Background(), ReassignIn{From: "@bob", Assign: "@alice"})
		if err != nil || out.Batch != "" || len(h.called) != 0 || out.Text() != "no open tasks assigned to @bob\n" {
			t.Fatalf("out %+v err %v", out, err)
		}
	})
	t.Run("no batch log", func(t *testing.T) {
		t.Parallel()
		h := &fakeReassignHost{plan: ReassignPlan{Tasks: tasks, Store: store.SourceRepo}}
		out, err := ops{env: fakeCoordEnv{h: h}}.reassign(context.Background(), ReassignIn{From: "@bob", Assign: "@alice"})
		if err != nil || out.Batch == "" || len(h.called) != 2 || out.Lines[len(out.Lines)-1] != noBatchLogNote {
			t.Fatalf("out %+v err %v", out, err)
		}
	})
}
