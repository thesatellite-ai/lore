package task_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// priorities counts the tasks per priority among ids.
func priorities(h *opstest.H, ids []string) map[string]int {
	h.T.Helper()
	m := map[string]int{}
	for _, id := range ids {
		_, r := get(h, schema.TableTasks, id)
		m[field(r, "priority")]++
	}
	return m
}

// TestBulkEditPreviewConfirm (OV22, TASK 18.1, XQ2): task edit --where over
// more tasks than batch.preview_rows asks first (E_NEEDS_CONFIRM) and
// writes nothing; --yes writes every match under one batch id; under the
// limit it writes at once; a match of none says so and writes nothing;
// tasks outside the filter are never touched.
func TestBulkEditPreviewConfirm(t *testing.T) {
	h := opstest.New(t)
	var hits []string
	for i := range 21 { // one over the default preview_rows (20)
		hits = append(hits, add(h, "bulk "+strconv.Itoa(i), `"priority":"low"`))
	}
	other := add(h, "other", `"priority":"medium"`)

	in := `{"where":["priority=low"],"priority":"high"}`
	h.Fails(opstask.OpEdit, in, errs.NeedsConfirm)
	if got := priorities(h, hits); got["low"] != 21 {
		t.Fatalf("an unconfirmed bulk edit wrote: %v", got)
	}
	res := h.MustYes(opstask.OpEdit, in)
	if got := priorities(h, hits); got["high"] != 21 {
		t.Errorf("confirmed bulk edit: %v", got)
	}
	if _, r := get(h, schema.TableTasks, other); field(r, "priority") != "medium" {
		t.Errorf("a task outside --where changed: %s", field(r, "priority"))
	}
	batch := h.Field(res, "id") // a bulk edit answers with its bat_ id
	evs, err := h.App.BatchEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		if ev.Batch.String() == batch && ev.Table == schema.TableTasks {
			n++
		}
	}
	if batch == "" || n != 21 {
		js, _ := json.Marshal(res.Value().Interface())
		t.Errorf("batch %q holds %d task events, want 21 (%s)", batch, n, js)
	}

	// Under a raised limit the same edit needs no confirmation.
	h.MustYes(settings.OpPolicySet, `{"key":"batch.preview_rows","value":"50"}`)
	h.Must(opstask.OpEdit, `{"where":["priority=high"],"priority":"urgent"}`)
	if got := priorities(h, hits); got["urgent"] != 21 {
		t.Errorf("under the limit: %v", got)
	}
	// No match: a line, no write, no error.
	if out := opstest.Text(h.Must(opstask.OpEdit, `{"where":["title=nothing"],"priority":"low"}`)); out != "no task matches --where; nothing changed\n" {
		t.Errorf("no match: %q", out)
	}
	// id and --where together, and --dry-run without --where, are refused.
	h.Fails(opstask.OpEdit, `{"id":"`+other+`","where":["priority=low"],"priority":"high"}`, errs.InvalidInput)
	h.Fails(opstask.OpEdit, `{"id":"`+other+`","dry_run":true,"priority":"high"}`, errs.InvalidInput)
}
