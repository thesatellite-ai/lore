package task

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
)

// TestSLADeferredTime: deferred time is left out of the SLA, blocked time
// is not (18.4); the deferred_until history splits the span (OV47).
func TestSLADeferredTime(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	hr := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Hour) }
	txt := func(x time.Time) string { return x.Format(time.RFC3339) }
	hours := map[string]int64{schema.TaskPriorityUrgent: 48}
	now := hr(60)
	cases := []struct {
		name     string
		c        SLAClock
		deferred time.Duration
		want     string
	}{
		{"no deferral: 60h of 48h", SLAClock{}, 0, SLABreached},
		{"deferred at creation for 20h, no history: 40h is 83%", SLAClock{DeferredUntil: hr(20)}, 20 * time.Hour, SLAAtRisk},
		{"deferred at 10h until 30h, by history", SLAClock{History: []HistoryEntry{
			{At: txt(hr(10)), Field: fieldDeferred, To: txt(hr(30))},
		}, DeferredUntil: hr(30)}, 20 * time.Hour, SLAAtRisk},
		{"deferral cleared early at 15h", SLAClock{History: []HistoryEntry{
			{At: txt(hr(10)), Field: fieldDeferred, To: txt(hr(30))},
			{At: txt(hr(15)), Field: fieldDeferred, From: txt(hr(30))},
		}}, 5 * time.Hour, SLABreached},
		{"deferred past now counts up to now", SLAClock{DeferredUntil: hr(100)}, 60 * time.Hour, SLAOK},
		{"blocked time still counts", SLAClock{Status: schema.TaskStatusBlocked}, 0, SLABreached},
	}
	for _, tc := range cases {
		c := tc.c
		c.Priority, c.Created = schema.TaskPriorityUrgent, t0
		if got := deferredWithin(c, now); got != tc.deferred {
			t.Errorf("%s: deferred %v, want %v", tc.name, got, tc.deferred)
		}
		if got := SLAStateOf(c, hours, now); got != tc.want {
			t.Errorf("%s: state %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := SLAStateOf(SLAClock{Priority: schema.TaskPriorityLow, Created: t0}, hours, now); got != SLANone {
		t.Errorf("no SLA for the priority: %q", got)
	}
}

// TestSLALate (18.4, doctor's SLA line, later 470): open tasks only (done,
// cancelled and trashed rows never listed), at risk from 80%, breached past
// the limit, a priority without an SLA never listed, nothing with no SLA
// policy at all.
func TestSLALate(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	now := t0.Add(50 * time.Hour)
	hours := map[string]int64{schema.TaskPriorityUrgent: 48, schema.TaskPriorityHigh: 60}
	mk := func(id, prio, status string, deleted bool) row.Row {
		aid, err := ids.ParseAny(id)
		if err != nil {
			t.Fatal(err)
		}
		f := map[string]value.Value{}
		for k, v := range map[string]string{fieldPriority: prio, work.FieldStatus: status} {
			tv, terr := value.TextOf(v)
			if terr != nil {
				t.Fatal(terr)
			}
			f[k] = tv
		}
		return row.Row{ID: aid, CreatedAt: t0, Fields: f, Deleted: deleted}
	}
	const (
		late   = "tsk_0e45506e64357c94a87e70ef1c6bb001" // urgent 50h of 48h: breached
		risk   = "tsk_0e45506e64357c94a87e70ef1c6bb002" // high 50h of 60h (83%): at risk
		lowp   = "tsk_0e45506e64357c94a87e70ef1c6bb003" // low: no SLA
		done   = "tsk_0e45506e64357c94a87e70ef1c6bb004"
		cancel = "tsk_0e45506e64357c94a87e70ef1c6bb005"
		gone   = "tsk_0e45506e64357c94a87e70ef1c6bb006"
		block  = "tsk_0e45506e64357c94a87e70ef1c6bb007" // blocked still counts
	)
	rows := []row.Row{
		mk(late, schema.TaskPriorityUrgent, schema.TaskStatusTodo, false),
		mk(risk, schema.TaskPriorityHigh, schema.TaskStatusInProgress, false),
		mk(lowp, schema.TaskPriorityLow, schema.TaskStatusTodo, false),
		mk(done, schema.TaskPriorityUrgent, schema.TaskStatusDone, false),
		mk(cancel, schema.TaskPriorityUrgent, schema.TaskStatusCancelled, false),
		mk(gone, schema.TaskPriorityUrgent, schema.TaskStatusTodo, true),
		mk(block, schema.TaskPriorityUrgent, schema.TaskStatusBlocked, false),
	}
	ar, br := SLALate(rows, hours, now)
	if len(ar) != 1 || ar[0] != risk {
		t.Errorf("at risk %v, want [%s]", ar, risk)
	}
	if len(br) != 2 || br[0] != late || br[1] != block {
		t.Errorf("breached %v, want [%s %s]", br, late, block)
	}
	if ar, br = SLALate(rows, nil, now); ar != nil || br != nil {
		t.Errorf("no policy: %v %v", ar, br)
	}
}
