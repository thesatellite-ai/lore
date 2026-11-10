package task_test

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/timeentry"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// TestTimeFlow walks the person path of TIME_TRACKING_DESIGN 2.1/2.3/2.5:
// start opens a status span, stop and done close it, manual log with its
// refusals, rm asks then tombstones, restore undoes, show adds it up.
func TestTimeFlow(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	base := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	var off atomic.Int64
	h := opstest.New(t, repoClient(false), func(o *wire.Options) { // time data lives in internal stores only (PC-LEAK-1)
		o.Wall = func() time.Time { return base.Add(time.Duration(off.Load())) }
	})
	id := add(h, "Rate limit", "")
	h.Must(opstask.OpStart, `{"id":"`+id+`"}`)
	_, r := get(h, schema.TableTasks, id)
	if n := len(opstask.TimeOpen(r)); n != 1 {
		t.Fatalf("open spans after start = %d", n)
	}
	// a second start by the same person adds nothing
	h.Must(opstask.OpStart, `{"id":"`+id+`"}`)
	_, r = get(h, schema.TableTasks, id)
	if n := len(opstask.TimeOpen(r)); n != 1 {
		t.Fatalf("open spans after second start = %d", n)
	}
	off.Store(int64(2 * time.Hour))
	h.Must(opstask.OpTimeStop, `{"id":"`+id+`"}`)
	_, r = get(h, schema.TableTasks, id)
	if len(opstask.TimeOpen(r)) != 0 || field(r, "status") != schema.TaskStatusInProgress {
		t.Fatalf("stop: open %d status %s", len(opstask.TimeOpen(r)), field(r, "status"))
	}
	if es := opstask.TimeEntries(r); len(es) != 1 || es[0].Seconds != 7200 || es[0].Source != timeentry.SourceStatus {
		t.Fatalf("entries after stop: %+v", es)
	}
	h.Fails(opstask.OpTimeStop, `{"id":"`+id+`"}`, errs.NotFound)
	// restart 1m later and finish: coalesced into the first entry (TQ4)
	off.Store(int64(2*time.Hour + time.Minute))
	h.Must(opstask.OpStart, `{"id":"`+id+`"}`)
	off.Store(int64(3 * time.Hour))
	h.Must(opstask.OpDone, `{"id":"`+id+`"}`)
	_, r = get(h, schema.TableTasks, id)
	es := opstask.TimeEntries(r)
	if len(es) != 2 || es[0].Seconds != 3*3600 || !es[1].Deleted() || !es[1].HasFlag(timeentry.FlagCoalesced) {
		t.Fatalf("coalesce: %+v", es)
	}
	// manual entries
	h.Fails(opstask.OpTimeLog, `{"id":"`+id+`","duration":"0"}`, errs.DurationBad)
	h.Fails(opstask.OpTimeLog, `{"id":"`+id+`","duration":"1d"}`, errs.DurationBad)
	h.Fails(opstask.OpTimeLog, `{"id":"`+id+`","duration":"-5m"}`, errs.DurationBad)
	h.Fails(opstask.OpTimeLog, `{"id":"`+id+`","duration":"25h"}`, errs.TimeTooLong)
	h.Fails(opstask.OpTimeLog, `{"id":"`+id+`","duration":"1h","at":"2026-11-17T14:00:00"}`, errs.InvalidInput)
	h.Fails(opstask.OpTimeLog, `{"id":"`+id+`","duration":"1h","note":"a\nb"}`, errs.InvalidInput)
	res := h.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"1h30m","note":"call","at":"2026-11-17"}`)
	eid := h.ID(res)
	show := h.Must(opstask.OpTimeShow, `{"id":"`+id+`"}`)
	js := h.JSON(show)
	if !strings.Contains(js, `"effort_seconds":16200`) || !strings.Contains(js, `"manual_seconds":5400`) || !strings.Contains(js, `"elapsed_seconds":10800`) {
		t.Fatalf("show: %s", js)
	}
	h.Fails(opstask.OpTimeRm, `{"id":"`+eid+`"}`, errs.NeedsConfirm)
	if _, err := h.RunYes(opstask.OpTimeRm, `{"id":"`+eid+`"}`); err != nil {
		t.Fatal(err)
	}
	if js = h.JSON(h.Must(opstask.OpTimeShow, `{"id":"`+id+`"}`)); !strings.Contains(js, `"effort_seconds":10800`) {
		t.Fatalf("after rm: %s", js)
	}
	h.Must(opstask.OpTimeRestore, `{"id":"`+eid+`"}`)
	// edit by unique suffix: the auto entry keeps orig and is flagged edited
	auto := es[0].ID.String()
	h.Must(opstask.OpTimeEdit, `{"id":"`+auto[len(auto)-8:]+`","duration":"1h"}`)
	_, r = get(h, schema.TableTasks, id)
	e := opstask.TimeEntries(r)[0]
	if e.Seconds != 3600 || !e.HasFlag(timeentry.FlagEdited) || e.Orig.Seconds != 3*3600 {
		t.Fatalf("edit: %+v", e)
	}
	h.Fails(opstask.OpTimeRestore, `{"id":"`+es[1].ID.String()+`"}`, errs.Refused)
}
