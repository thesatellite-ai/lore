package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// More actors of the merge chain matrix (actA, actB: time_people_test.go).
const (
	actC = "act_0e45506e64357c94a87e70ef1c6bb0c3" // merged into B (chain)
	actD = "act_0e45506e64357c94a87e70ef1c6bb0d4" // D and E merged into each other (cycle)
	actE = "act_0e45506e64357c94a87e70ef1c6bb0e5"
)

// mergeInto sets actor id's merged_into.
func mergeInto(t *testing.T, h *opstest.H, id, to string) {
	t.Helper()
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.App.Update(context.Background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableActors, ID: aid},
		map[string]value.Value{"merged_into": text(t, to)}); err != nil {
		t.Fatal(err)
	}
}

// TestTimeFollowsMergedInto (FA-TASK-6, TIME 11.11, later 43): time show
// maps each entry's actor to the end of its merged_into chain (C -> B -> A
// shows one worker A with all three entries; a D <-> E cycle stops instead
// of looping and keeps the entry); stored entries keep their actor and the
// WHO column names the stored actor; task list --worked-by A finds a task
// whose only time is C's. Needs actors.merged_into (G-TASK-1 schema patch).
func TestTimeFollowsMergedInto(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := opstest.New(t, repoClient(false))
	for _, a := range []struct{ id, name string }{{actA, "Ann"}, {actB, "Ann Old"}, {actC, "Ann Older"}, {actD, "Dee"}, {actE, "Eve"}} {
		actorRow(t, h, a.id, a.name)
	}
	mergeInto(t, h, actB, actA)
	mergeInto(t, h, actC, actB)
	mergeInto(t, h, actD, actE)
	mergeInto(t, h, actE, actD)
	id := add(h, "merged time", "")
	onlyC := add(h, "only C", "")
	h.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"1h","at":"2026-11-17","by":"`+actC+`"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"30m","at":"2026-11-17","by":"`+actB+`"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"15m","at":"2026-11-17","by":"`+actD+`"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+onlyC+`","duration":"10m","at":"2026-11-17","by":"`+actC+`"}`)
	byActor := map[string]int64{}
	for _, w := range timeOut(t, h, `{"id":"`+id+`"}`).Data.Workers {
		byActor[w.Actor] += w.TotalSeconds
	}
	if byActor[actA] != 5400 || byActor[actB] != 0 || byActor[actC] != 0 || byActor[actD]+byActor[actE] != 900 {
		t.Errorf("workers %v", byActor)
	}
	_, r := get(h, schema.TableTasks, id)
	stored := map[string]bool{}
	for _, e := range opstask.TimeEntries(r) {
		stored[e.ActorID.String()] = true
	}
	if !stored[actC] || !stored[actB] || stored[actA] {
		t.Errorf("entries rewritten: %v", stored)
	}
	if txt := opstest.Text(h.Must(opstask.OpTimeShow, `{"id":"`+id+`","entries":true}`)); !strings.Contains(txt, "Ann Older") {
		t.Errorf("WHO column does not name the stored actor:\n%s", txt)
	}
	if js := h.JSON(h.Must(opstask.OpList, `{"worked_by":"`+actA+`"}`)); !strings.Contains(js, onlyC) || !strings.Contains(js, id) {
		t.Errorf("--worked-by A misses C's time: %s", js)
	}
}
