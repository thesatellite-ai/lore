package task_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestDuplicateCascadeCopies (TASK 8.2, OV15): relate --duplicates copies
// the duplicate's comments and the checklist texts the original lacks onto
// the original (an item both have is not doubled, a removed item is not
// copied, a ticked item stays ticked), keeps the duplicate's own comments,
// and cancels the duplicate with close_reason duplicate. A duplicate
// cycle is E_CYCLE with nothing copied.
func TestDuplicateCascadeCopies(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	orig := add(h, "original", "")
	dup := add(h, "duplicate", "")
	h.Must(opstask.OpChecklistAdd, `{"id":"`+orig+`","text":"shared"}`)
	for _, s := range []string{"shared", "only on dup", "ticked on dup", "removed on dup"} {
		h.Must(opstask.OpChecklistAdd, `{"id":"`+dup+`","text":`+opstest.Quote(s)+`}`)
	}
	h.Must(opstask.OpChecklistTick, `{"id":"`+dup+`","item":"3"}`)
	h.MustYes(opstask.OpChecklistRm, `{"id":"`+dup+`","item":"4"}`)
	h.MustYes("comment.add", `{"on_table":"tasks","on_id":"`+dup+`","body":"repro steps on the dup"}`)

	out := opstest.Text(h.Must(opstask.OpRelate, `{"id":"`+dup+`","duplicates":"`+orig+`"}`))
	if !strings.Contains(out, "copied to "+orig+": 1 comment(s), 2 checklist item(s)") {
		t.Fatalf("relate --duplicates must report what it copied: %q", out)
	}

	_, o := get(h, schema.TableTasks, orig)
	var texts []string
	ticked := map[string]bool{}
	for _, it := range opstask.Checklist(o) {
		if it.Deleted() {
			continue
		}
		texts = append(texts, it.Text)
		ticked[it.Text] = it.Done
	}
	slices.Sort(texts)
	if want := []string{"only on dup", "shared", "ticked on dup"}; !slices.Equal(texts, want) {
		t.Fatalf("original checklist %v, want %v (no double, no removed item)", texts, want)
	}
	if !ticked["ticked on dup"] || ticked["only on dup"] {
		t.Fatalf("tick state must travel with the item: %v", ticked)
	}
	for _, on := range []string{orig, dup} {
		if got := h.JSON(h.Must("comment.list", `{"on_table":"tasks","on_id":"`+on+`"}`)); !strings.Contains(got, "repro steps on the dup") {
			t.Fatalf("comment missing on %s (copied to the original, kept on the duplicate): %s", on, got)
		}
	}
	_, d := get(h, schema.TableTasks, dup)
	if field(d, "status") != schema.TaskStatusCancelled || field(d, schema.FieldTaskCloseReason) != "duplicate" {
		t.Fatalf("duplicate must be cancelled with close_reason duplicate: %s / %s", field(d, "status"), field(d, schema.FieldTaskCloseReason))
	}

	// orig duplicating dup would loop: refused, nothing copied back.
	h.Fails(opstask.OpRelate, `{"id":"`+orig+`","duplicates":"`+dup+`"}`, errs.Cycle)
	if got := h.JSON(h.Must("comment.list", `{"on_table":"tasks","on_id":"`+dup+`"}`)); strings.Count(got, "repro steps on the dup") != 1 {
		t.Fatalf("a refused cycle copied comments back: %s", got)
	}
}
