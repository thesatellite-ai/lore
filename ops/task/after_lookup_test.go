package task_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestRequiredAfterLookup (OV29): the checklist item/text and the recur
// rule are required, but checked by the handler after the task lookup, so
// a bad task id answers E_NOT_FOUND first and a good id with the value
// missing answers E_INVALID_INPUT. The registry still lists them as
// required (docs and OpenAPI read FieldInfo.Required).
func TestRequiredAfterLookup(t *testing.T) {
	h := opstest.New(t)
	id := add(h, "has a checklist", `"due":"2026-11-30","recur":"every day"`)
	h.Must(opstask.OpChecklistAdd, `{"id":"`+id+`","text":"one"}`)
	const nope = "tsk_0e45506e64357c94a87e70ef1c6bbb99"
	cases := []struct {
		op, in string
		code   errs.Code
	}{
		// missing value + unknown task: the lookup speaks first.
		{opstask.OpChecklistEdit, `{"id":"` + nope + `"}`, errs.NotFound},
		{opstask.OpChecklistEdit, `{"id":"` + nope + `","item":"1"}`, errs.NotFound},
		{opstask.OpChecklistTick, `{"id":"` + nope + `"}`, errs.NotFound},
		{opstask.OpChecklistUntick, `{"id":"` + nope + `"}`, errs.NotFound},
		{opstask.OpChecklistRm, `{"id":"` + nope + `"}`, errs.NotFound},
		{opstask.OpRecurEdit, `{"id":"` + nope + `"}`, errs.NotFound},
		// a real task with the value missing (or blank): a bad input.
		{opstask.OpChecklistEdit, `{"id":"` + id + `"}`, errs.InvalidInput},
		{opstask.OpChecklistEdit, `{"id":"` + id + `","item":"1"}`, errs.InvalidInput},
		{opstask.OpChecklistEdit, `{"id":"` + id + `","item":"1","text":"  "}`, errs.InvalidInput},
		{opstask.OpChecklistTick, `{"id":"` + id + `"}`, errs.InvalidInput},
		{opstask.OpRecurEdit, `{"id":"` + id + `"}`, errs.InvalidInput},
	}
	for _, c := range cases {
		h.Fails(c.op, c.in, c.code)
	}
	// With every value given they work.
	h.Must(opstask.OpChecklistEdit, `{"id":"`+id+`","item":"1","text":"renamed"}`)

	want := map[string][]string{
		opstask.OpChecklistEdit: {"item", "text"},
		opstask.OpChecklistTick: {"item"},
		opstask.OpRecurEdit:     {"rule"},
	}
	for name, fields := range want {
		e, ok := h.App.Registry().Lookup(name)
		if !ok {
			t.Fatalf("no op %s", name)
		}
		for _, f := range fields {
			found := false
			for _, fi := range e.InputFields() {
				if fi.Name == f {
					found = true
					if !fi.Required || !fi.AfterLookup {
						t.Errorf("%s %s: Required=%v AfterLookup=%v, want both", name, f, fi.Required, fi.AfterLookup)
					}
				}
			}
			if !found {
				t.Errorf("%s: no field %s", name, f)
			}
		}
	}
}
