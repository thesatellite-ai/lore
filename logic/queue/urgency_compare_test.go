package queue

import (
	"slices"
	"sort"
	"testing"
	"time"
)

// TSQ13 old-behaviour comparison (TASK 13.4): a fixture set ordered by the
// v0.1.17 rule (effective priority with the 24-hour aging step, kept here
// verbatim as legacyOrder) and by the urgency score. wantChanges lists
// every position that changed with the term that caused it; the release
// notes quote this list under "Behaviour changes". A changed order is a
// behaviour change, never re-accepted as noise.

var cmpNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func cmpFixture() []Task {
	d := func(days int) time.Time { return cmpNow.Add(-time.Duration(days) * 24 * time.Hour) }
	return []Task{
		{ID: "t01", Status: StatusTodo, Priority: PriorityUrgent, CreatedAt: d(1)},
		{ID: "t02", Status: StatusTodo, Priority: PriorityHigh, CreatedAt: d(3)},
		{ID: "t03", Status: StatusTodo, Priority: PriorityLow, CreatedAt: d(2), Due: cmpNow.Add(12 * time.Hour)},
		{ID: "t04", Status: StatusTodo, Priority: PriorityMedium, CreatedAt: d(90)},
		{ID: "t05", Status: StatusInProgress, Priority: PriorityMedium, CreatedAt: d(5)},
		{ID: "t06", Status: StatusTodo, Priority: PriorityLow, CreatedAt: d(10), NextTag: true},
		{ID: "t07", Status: StatusTodo, Priority: PriorityHigh, CreatedAt: d(0)},
		{ID: "t08", Status: StatusTodo, Priority: PriorityMedium, CreatedAt: d(4)},
		{ID: "t09", Status: StatusTodo, Priority: PriorityLow, CreatedAt: d(6)},
		{ID: "t10", Status: StatusTodo, Priority: PriorityMedium, CreatedAt: d(7), After: []Dep{{TaskID: "t09", Until: UntilDone}}},
	}
}

// legacyOrder is v0.1.17's queue.Next order (queue.go:194, 327 before TSQ13).
func legacyOrder(tasks []Task, now time.Time) []string {
	byID := map[string]Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	eff := map[string]int{}
	for _, t := range tasks {
		p := t.Priority
		if p == 0 {
			p = PriorityMedium
		}
		if !t.CreatedAt.IsZero() && now.Sub(t.CreatedAt) > 24*time.Hour && p < PriorityUrgent {
			p++
		}
		eff[t.ID] = p
	}
	for range tasks {
		changed := false
		for _, t := range tasks {
			for _, dep := range t.After {
				if _, ok := byID[dep.TaskID]; ok && eff[dep.TaskID] < eff[t.ID] {
					eff[dep.TaskID] = eff[t.ID]
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	var ready []Task
	for _, t := range tasks {
		if _, ok := depsSatisfied(t, byID); ok {
			ready = append(ready, t)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool {
		a, b := ready[i], ready[j]
		if eff[a.ID] != eff[b.ID] {
			return eff[a.ID] > eff[b.ID]
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	out := make([]string, 0, len(ready))
	for _, t := range ready {
		out = append(out, t.ID)
	}
	return out
}

func TestUrgencyVersusV017Order(t *testing.T) {
	tasks := cmpFixture()
	old := legacyOrder(tasks, cmpNow)
	res := Next(Input{Tasks: tasks, Now: cmpNow})
	var cur []string
	for _, r := range res.Ready {
		cur = append(cur, r.ID)
	}
	wantOld := []string{"t02", "t01", "t04", "t09", "t05", "t08", "t07", "t06", "t03"}
	wantNew := []string{"t06", "t03", "t01", "t09", "t05", "t02", "t07", "t04", "t08"}
	// Behaviour changes (position old → new, the term that moved it):
	//   t06 8 → 1  next_tag +15.00 (tagged next; v0.1.17 had no such term)
	//   t03 9 → 2  due +11.58 (due in 12 hours; v0.1.17 ignored due_at)
	//   t01 2 → 3  overtaken by t06, t03; now ahead of t02 (no aging step lifts high to urgent)
	//   t02 1 → 6  lost the 24 h aging step (high → urgent); age is +0.10 now
	//   t04 3 → 8  age capped at +2.00 replaces the old +1 priority step
	//   t08 6 → 9  age +0.13 instead of a full priority step
	//   t09 4 → 4, t05 5 → 5, t07 7 → 7 unchanged (blocking +4.00, in_progress +4.00 hold them)
	if !equal(old, wantOld) {
		t.Fatalf("v0.1.17 order changed: %v", old)
	}
	if !equal(cur, wantNew) {
		t.Fatalf("urgency order %v; update the change list above only after reading why", cur)
	}
}

func equal(a, b []string) bool { return slices.Equal(a, b) }

func TestLeavesOnlyAndAncestorWaits(t *testing.T) {
	tasks := []Task{
		{ID: "up", Status: StatusTodo},
		{ID: "p", Status: StatusTodo, After: []Dep{{TaskID: "up", Until: UntilDone}}},
		{ID: "c", Status: StatusTodo, ParentID: "p"},
		{ID: "g", Status: StatusTodo, Gated: true},
		{ID: "gc", Status: StatusTodo, ParentID: "g"},
	}
	res := Next(Input{Tasks: tasks, Now: cmpNow})
	var ready []string
	for _, r := range res.Ready {
		ready = append(ready, r.ID)
	}
	if !equal(ready, []string{"up"}) {
		t.Fatalf("ready %v", ready)
	}
	reasons := map[string]string{}
	for _, w := range res.Waiting {
		reasons[w.Task.ID] = w.Reason
	}
	if reasons["c"] == "" || reasons["gc"] == "" || reasons["p"] != "" || reasons["g"] != "" {
		t.Fatalf("waiting %v", reasons)
	}
}
