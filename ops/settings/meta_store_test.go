package settings_test

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaSetNamesItsStore: meta set and meta unset on one row name the
// store the row lives in, as every other row write does (the machine
// sweep saw `"store": ""` from both).
func TestMetaSetNamesItsStore(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"client","type":"text"}`)
	id := h.ID(h.MustYes("memory.add", `{"body":"x"}`))
	for _, step := range []struct{ op, in string }{
		{settings.OpMetaSet, `{"id":"` + id + `","pair":"client=acme"}`},
		{settings.OpMetaUnset, `{"id":"` + id + `","key":"client"}`},
	} {
		var out struct {
			Store string `json:"store"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.MustYes(step.op, step.in))), &out); err != nil {
			t.Fatal(err)
		}
		if out.Store == "" {
			t.Errorf("%s answered with no store", step.op)
		}
	}
}
