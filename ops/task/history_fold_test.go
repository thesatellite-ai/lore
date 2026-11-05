package task_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestNextCoreWriteFoldsHistory (OV59, later 520): a writer outside the
// task ops (task reassign through the coordinator appends history without
// applying tasks.history_max) can leave a task over the cap; the next core
// write folds it: the newest history_max-1 entries stay, everything older
// becomes one summary entry whose counts add up, and the new change is
// recorded.
func TestNextCoreWriteFoldsHistory(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpPolicySet, `{"key":"tasks.history_max","value":"3"}`)
	id := add(h, "reassigned a lot", "")
	// six reassign entries, written straight to the row as the coordinator does.
	parts := make([]string, 6)
	for i := range parts {
		parts[i] = fmt.Sprintf(`{"id":"the_0000000000000000000000000000%04d","at":"2026-10-0%dT10:00:00Z","by":"lore","field":"assigned_to_actor_id","to":"act_%d"}`, i+1, i+1, i)
	}
	hist, err := value.JSONOf([]byte("[" + strings.Join(parts, ",") + "]"))
	if err != nil {
		t.Fatal(err)
	}
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.App.Update(context.Background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableTasks, ID: aid},
		map[string]value.Value{schema.FieldTaskHistory: hist}); err != nil {
		t.Fatal(err)
	}
	_, r := get(h, schema.TableTasks, id)
	if n := len(opstask.History(r)); n < 6 {
		t.Fatalf("seeded history has %d entries", n)
	}
	h.Must(opstask.OpEdit, `{"id":"`+id+`","priority":"high"}`)
	_, r = get(h, schema.TableTasks, id)
	got := opstask.History(r)
	if len(got) != 3 {
		t.Fatalf("after the core write: %d entries, want 3: %+v", len(got), got)
	}
	var sum *opstask.HistoryEntry
	total := 0
	for i := range got {
		if got[i].Field == "_summary" {
			sum = &got[i]
			for _, c := range got[i].Counts {
				total += c
			}
		}
	}
	if sum == nil {
		t.Fatalf("no summary entry: %+v", got)
	}
	// 6 seeded + the create's own entries + the priority change, minus the 2 kept.
	if total < 5 || !strings.Contains(sum.Note, "folded") {
		t.Fatalf("summary %+v (total %d)", *sum, total)
	}
	last := got[len(got)-1]
	if last.Field != "priority" || last.To != "high" {
		t.Fatalf("newest entry %+v, want the priority change", last)
	}
}
