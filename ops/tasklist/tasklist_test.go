package tasklist_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
)

// addID adds a tasklist and returns its id (the add output is v0.1.17's
// printJSON(constants.KindTasklistAdd, row): kind tasklist.add).
func addID(t *testing.T, h *opstest.H, in string) string {
	t.Helper()
	var w struct {
		Kind string
		Data struct {
			ID        string `json:"id"`
			StatusStr string `json:"status_str"`
		}
	}
	res := h.Must("tasklist.add", in)
	if err := json.Unmarshal([]byte(h.JSON(res)), &w); err != nil {
		t.Fatal(err)
	}
	if w.Kind != "tasklist.add" || w.Data.StatusStr != "active" {
		t.Fatalf("add envelope %s (v0.1.17: kind tasklist.add, status_str active)", h.JSON(res))
	}
	if got := opstest.Text(res); got != "✓ "+w.Data.ID+"\n" {
		t.Fatalf("add text %q", got)
	}
	return w.Data.ID
}

// TestTasklistLifecycle: v0.1.17's lines ("✓ tlt_…", "TA  tlt_…" for list
// and show, search "<id> <title>", "✓ tasklist <id> updated"), status_str
// free text (any words, unicode), tombstone delete with confirmation.
func TestTasklistLifecycle(t *testing.T) {
	h := opstest.New(t)
	id := addID(t, h, `{"title":"Sprint 1","body":"the list"}`)
	arg := addID(t, h, `{"title":"Sprint 2","text":"positional body"}`)
	if got := opstest.Text(h.Must("tasklist.show", `{"id":"`+id+`"}`)); got != "TA  "+id+"\n" {
		t.Fatalf("show %q", got)
	}
	list := h.Must("tasklist.list", ``)
	if got := opstest.IDPattern(opstest.Text(list)); got != "TA  tlt_ID\nTA  tlt_ID\n" {
		t.Fatalf("list %q", got)
	}
	if !strings.Contains(h.JSON(list), `"kind":"taskList.list"`) || !strings.Contains(h.JSON(list), `"count":2`) {
		t.Fatalf("list json %s", h.JSON(list))
	}
	for _, status := range []string{"half done ✓", "blocked on QA", "x"} {
		res := h.Must("tasklist.edit", `{"id":"`+id+`","status":`+opstest.Quote(status)+`}`)
		if got := opstest.Text(res); got != "✓ tasklist "+id+" updated\n" {
			t.Fatalf("edit %q", got)
		}
		if !strings.Contains(h.JSON(h.Must("tasklist.show", `{"id":"`+id+`"}`)), `"status_str":`+opstest.Quote(status)) {
			t.Fatalf("status_str %q not stored", status)
		}
	}
	if got := opstest.Text(h.Must("tasklist.search", `{"query":"sprint"}`)); !strings.Contains(got, id+" Sprint 1\n") || !strings.Contains(got, arg+" Sprint 2\n") {
		t.Fatalf("search %q", got)
	}
	h.Fails("tasklist.delete", `{"id":"`+id+`"}`, errs.NeedsConfirm)
	h.MustYes("tasklist.delete", `{"id":"`+id+`"}`)
	if strings.Contains(opstest.Text(h.Must("tasklist.list", ``)), id) {
		t.Fatal("deleted tasklist listed")
	}
	h.Fails("tasklist.show", `{"id":"`+id+`"}`, errs.InTrash)
}

// TestTasklistRefusals: title and body required (v0.1.17 "body required"),
// body once, nothing to change, malformed / foreign ids.
func TestTasklistRefusals(t *testing.T) {
	h := opstest.New(t)
	for _, in := range []string{`{"title":"Sprint 2"}`, `{"title":"x","body":" \n"}`, `{"title":"  ","body":"b"}`, `{"title":"t","body":"a","text":"b"}`} {
		h.Fails("tasklist.add", in, errs.InvalidInput)
	}
	if msg := h.Fails("tasklist.add", `{"title":"Sprint 2"}`, errs.InvalidInput); !strings.Contains(msg, "body required") {
		t.Fatalf("v0.1.17 words: %s", msg)
	}
	id := addID(t, h, `{"title":"t","body":"b"}`)
	h.Fails("tasklist.edit", `{"id":"`+id+`"}`, errs.InvalidInput)
	for _, bad := range []string{"tlt_bad", "pln_01a1a684b528751aa2fc45745b154d16"} {
		h.Fails("tasklist.show", `{"id":"`+bad+`"}`, errs.InvalidID)
	}
}
