package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestEstimateNoDayUnit (OWNERQ-21): task add and edit refuse a day amount
// under points and hours, and every estimate while tasks.estimate_unit is
// the legacy days, each E_INVALID_INPUT with nothing written; a task's
// estimate stored before the switch is kept as typed (there is nothing to
// migrate: the row holds a plain number, the unit lives in the policy) and
// still prints in days; switching the policy to hours makes writes work
// again.
func TestEstimateNoDayUnit(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	kept := add(h, "estimated before", `"estimate":"3"`)
	for _, unit := range []string{"points", "hours"} {
		h.MustYes("policy.set", `{"key":"tasks.estimate_unit","value":"`+unit+`"}`)
		before := h.JSON(h.Must(opstask.OpList, `{}`))
		h.Fails(opstask.OpAdd, `{"title":"day amount","estimate":"2d"}`, errs.InvalidInput)
		h.Fails(opstask.OpEdit, `{"id":"`+kept+`","estimate":"2d"}`, errs.InvalidInput)
		if h.JSON(h.Must(opstask.OpList, `{}`)) != before {
			t.Fatalf("%s: a refused estimate wrote a row", unit)
		}
	}
	// a policy row from an earlier build: policy set refuses days now
	// (OWNERQ-21, G-META-8), so the legacy row is seeded as it is stored,
	// in a repo that has no estimate_unit row yet (a seed over the hours
	// row above would be a stale write)
	h = opstest.New(t, repoClient(false))
	kept = add(h, "estimated before", `"estimate":"3"`)
	h.Seed("policies", map[string]string{"key": `"tasks.estimate_unit"`, "value": `"days"`, "scope": `"repo"`})
	for _, raw := range []string{"2", "2d", "16h"} {
		_, err := h.Run(opstask.OpAdd, `{"title":"x","estimate":"`+raw+`"}`)
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "no day unit") || !strings.Contains(hintOf(err), "--key tasks.estimate_unit --value hours") {
			t.Fatalf("add --estimate %s under days: %v / %s", raw, err, hintOf(err))
		}
		h.Fails(opstask.OpEdit, `{"id":"`+kept+`","estimate":"`+raw+`"}`, errs.InvalidInput)
	}
	add(h, "no estimate", "") // a write without an estimate is unaffected
	if show := opstest.Text(h.Must(opstask.OpShow, `{"id":"`+kept+`"}`)); !strings.Contains(show, "3d") {
		t.Fatalf("the stored estimate no longer reads under days:\n%s", show)
	}
	h.MustYes("policy.set", `{"key":"tasks.estimate_unit","value":"hours"}`)
	h.Must(opstask.OpEdit, `{"id":"`+kept+`","estimate":"90m"}`)
	if show := opstest.Text(h.Must(opstask.OpShow, `{"id":"`+kept+`"}`)); !strings.Contains(show, "1.5h") {
		t.Fatalf("hours estimate after the switch:\n%s", show)
	}
}
