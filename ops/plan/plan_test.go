package plan_test

import (
	"encoding/json"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/plan"
	"github.com/thesatellite-ai/lore/schema"
)

// TestUpcastStatus is the TSQ9 matrix: a v0.1.17 free-form status_str
// becomes a closed-set status (members kept, any case and padding;
// "active" means in_progress; anything else, empty included, is draft).
func TestUpcastStatus(t *testing.T) {
	cases := map[string]string{
		"": "draft", "draft": "draft", "approved": "approved", "in_progress": "in_progress",
		"done": "done", "dropped": "dropped", "active": "in_progress", " Active ": "in_progress",
		"DONE": "done", "half way": "draft", "in progress": "draft", "✓ shipped": "draft",
	}
	for in, want := range cases {
		if got := plan.UpcastStatus(in); got != want {
			t.Errorf("UpcastStatus(%q) = %q, want %q", in, got, want)
		}
	}
	for _, s := range schema.PlanStatuses() {
		if got := plan.UpcastStatus(s); got != s {
			t.Errorf("closed-set member %q upcast to %q", s, got)
		}
	}
}

// TestPlanLifecycle: v0.1.17's lines ("✓ pln_…", "PL  pln_…" for list),
// show prints the id, title, status, task count and body, search "<id> <title>") and envelope kinds, status draft by default.
func TestPlanLifecycle(t *testing.T) {
	h := opstest.New(t)
	add := h.Must("plan.add", `{"title":"Q4 plan","body":"ship it"}`)
	var shown struct {
		Kind string
		Data struct{ ID, Title, Body, Status string }
	}
	if err := json.Unmarshal([]byte(h.JSON(add)), &shown); err != nil {
		t.Fatal(err)
	}
	id := shown.Data.ID
	if got := opstest.Text(add); got != "✓ "+id+"\n" {
		t.Fatalf("add %q", got)
	}
	if shown.Kind != "plan.show" || shown.Data.Status != "draft" || shown.Data.Body != "ship it" {
		t.Fatalf("add json %s", h.JSON(add))
	}
	arg := h.Must("plan.add", `{"title":"Arg plan","text":"body as arg"}`) // positional body
	if !strings.Contains(h.JSON(arg), `"body":"body as arg"`) {
		t.Fatalf("positional body: %s", h.JSON(arg))
	}
	ps := h.Must("plan.show", `{"id":"`+id+`"}`) // INTEG-AUDIT-2: ends with the writers line
	if got := opstest.Text(ps); got != "PL  "+id+"\n  title: Q4 plan\n  status: draft\n  tasks: 0\n\nship it\n"+contenttest.WriterLine(t, h.JSON(ps), "data") {
		t.Fatalf("show %q", got)
	}
	list := opstest.IDPattern(opstest.Text(h.Must("plan.list", ``)))
	if list != "pln_ID  Q4 plan [draft]  (0 tasks)\npln_ID  Arg plan [draft]  (0 tasks)\n" {
		t.Fatalf("list %q", list)
	}
	if got := opstest.Text(h.Must("plan.search", `{"query":"ship"}`)); got != id+" Q4 plan\n" {
		t.Fatalf("search %q", got)
	}
	h.Fails("plan.delete", `{"id":"`+id+`"}`, errs.NeedsConfirm)
	h.MustYes("plan.delete", `{"id":"`+id+`"}`)
	if strings.Contains(opstest.Text(h.Must("plan.list", ``)), id) {
		t.Fatal("deleted plan listed")
	}
	h.Fails("plan.show", `{"id":"`+id+`"}`, errs.InTrash)
}

// TestPlanAddEditRefusals: title and body required, body given once, the
// status closed set on edit, an edit with nothing to change.
func TestPlanAddEditRefusals(t *testing.T) {
	h := opstest.New(t)
	cases := []struct {
		in   string
		code errs.Code
	}{
		{`{"title":"Bare"}`, errs.InvalidInput},
		{`{"title":"Bare","body":"   "}`, errs.InvalidInput},
		{`{"title":"  ","body":"x"}`, errs.InvalidInput},
		{`{"body":"x"}`, errs.InvalidInput},
		{`{"title":"t","body":"a","text":"b"}`, errs.InvalidInput},
		{`{"title":"t","body":"b","created_by":"act_0e45506e64357c94a87e70ef1c6bbba9"}`, errs.InvalidInput}, // no hand-set creator (INTEG-AUDIT-1)
	}
	for _, c := range cases {
		h.Fails("plan.add", c.in, c.code)
	}
	add := h.Must("plan.add", `{"title":"t","body":"b"}`)
	var w struct{ Data struct{ ID string } }
	if err := json.Unmarshal([]byte(h.JSON(add)), &w); err != nil {
		t.Fatal(err)
	}
	id := w.Data.ID
	for _, bad := range []string{"active", "in progress", "Done"} {
		h.Fails("plan.edit", `{"id":"`+id+`","status":"`+bad+`"}`, errs.InvalidInput)
	}
	h.Fails("plan.edit", `{"id":"`+id+`"}`, errs.InvalidInput)
	for _, s := range schema.PlanStatuses() {
		h.Must("plan.edit", `{"id":"`+id+`","status":"`+s+`"}`)
		if !strings.Contains(h.JSON(h.Must("plan.show", `{"id":"`+id+`"}`)), `"status":"`+s+`"`) {
			t.Fatalf("status %s not stored", s)
		}
	}
	h.Fails("plan.edit", `{"id":"pln_bad","title":"x"}`, errs.InvalidID)
}

// TestPlanLegacyStatusStr: a v0.1.17 row (status_str only) reads with the
// upcast status, and any edit moves status_str into status in the same
// write (TSQ9) unless --status names one.
func TestPlanLegacyStatusStr(t *testing.T) {
	h := opstest.New(t)
	for _, c := range []struct{ legacy, edit, want string }{
		{"active", `"title":"renamed"`, "in_progress"},
		{"free text", `"body":"new"`, "draft"},
		{"active", `"status":"done"`, "done"},
		{"approved", `"title":"x"`, "approved"},
	} {
		id := h.Seed(schema.TablePlans, map[string]string{"status_str": opstest.Quote(c.legacy)})
		before := h.JSON(h.Must("plan.show", `{"id":"`+id+`"}`))
		if !strings.Contains(before, `"status":"`+plan.UpcastStatus(c.legacy)+`"`) {
			t.Fatalf("%s: read %s", c.legacy, before)
		}
		h.Must("plan.edit", `{"id":"`+id+`",`+c.edit+`}`)
		after := h.JSON(h.Must("plan.show", `{"id":"`+id+`"}`))
		if !strings.Contains(after, `"status":"`+c.want+`"`) {
			t.Errorf("%s + %s: %s, want status %s", c.legacy, c.edit, after, c.want)
		}
		_, r, err := h.App.Get(t.Context(), engineRef(t, id))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := r.Fields[schema.FieldPlanStatusStr]; ok {
			t.Errorf("%s: status_str kept after edit", c.legacy)
		}
	}
}

// engineRef is the engine ref of plan id.
func engineRef(t *testing.T, id string) engine.Ref {
	t.Helper()
	pid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	return engine.Ref{Table: schema.TablePlans, ID: pid}
}
