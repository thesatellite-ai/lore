package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestEditAfterCycleMatrix: `task edit --after` refuses an edge that
// closes a dependency cycle (E_CYCLE, v0.1.17), whatever the :done /
// :merged suffix and wherever the tasks live, and writes nothing; an
// edge that closes no cycle is written. The edges are read back from the
// stored row files, which is where the check was blind before (depsOf
// only understood an engine-built value.JSON, never a decoded list).
func TestEditAfterCycleMatrix(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false), withOrg(t))
	toOrg := `"to":"` + orgStore + `"`
	cases := []struct {
		name string
		// build returns the task to edit and the --after value that must
		// be refused.
		build func() (string, string)
	}{
		{"self", func() (string, string) { a := add(h, "a", ""); return a, a }},
		{"self :done", func() (string, string) { a := add(h, "a", ""); return a, a + ":done" }},
		{"2-cycle", func() (string, string) {
			a := add(h, "a", "")
			b := add(h, "b", `"after":["`+a+`"]`)
			return a, b
		}},
		{"2-cycle :merged", func() (string, string) {
			a := add(h, "a", "")
			b := add(h, "b", `"after":["`+a+`:merged"]`)
			return a, b + ":merged"
		}},
		{"3-cycle", func() (string, string) {
			a := add(h, "a", "")
			b := add(h, "b", `"after":["`+a+`"]`)
			c := add(h, "c", `"after":["`+b+`:done"]`)
			return a, c
		}},
		{"3-cycle in a list", func() (string, string) {
			a := add(h, "a", "")
			free := add(h, "free", "")
			b := add(h, "b", `"after":["`+a+`"]`)
			c := add(h, "c", `"after":["`+b+`"]`)
			return a, free + "," + c
		}},
		{"across stores", func() (string, string) {
			a := add(h, "a", "")
			b := addID(h, h.Must(opstask.OpAdd, `{"title":"b",`+toOrg+`,"after":["`+a+`"]}`))
			c := add(h, "c", `"after":["`+b+`"]`)
			return a, c
		}},
		{"across stores, edit in org", func() (string, string) {
			a := addID(h, h.Must(opstask.OpAdd, `{"title":"a",`+toOrg+`}`))
			b := add(h, "b", `"after":["`+a+`"]`)
			return a, b + ":merged"
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, after := c.build()
			_, before := get(h, schema.TableTasks, id)
			_, err := h.Run(opstask.OpEdit, `{"id":"`+id+`","after":[`+opstest.Quote(after)+`]}`)
			if !errs.Has(err, errs.Cycle) {
				t.Errorf("edit %s --after %s: %v, want E_CYCLE", id, after, err)
			} else if !strings.Contains(err.Error(), "dependency cycle: "+id+" → ") {
				t.Errorf("message: %v", err)
			}
			if _, now := get(h, schema.TableTasks, id); field(now, "blocked_by") != field(before, "blocked_by") {
				t.Error("refused edit changed blocked_by")
			}
		})
	}
	// No cycle: a diamond is fine.
	a := add(h, "a", "")
	b := add(h, "b", `"after":["`+a+`"]`)
	c := add(h, "c", `"after":["`+a+`"]`)
	d := add(h, "d", "")
	h.Must(opstask.OpEdit, `{"id":"`+d+`","after":["`+b+`","`+c+`:merged"]}`)
}
