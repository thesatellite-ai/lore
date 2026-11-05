package tag_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// txt is the result's JSON text (enough to look for a field value).
func txt(t *testing.T, res op.Result) string {
	t.Helper()
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestTagEdit: by name and by id, globs normalised and cleared, a rename
// keeps the id, nothing to change and an unknown tag refuse (6.10).
func TestTagEdit(t *testing.T) {
	h := opstest.Bare(t)
	h.SeedProjectID(v017Project)
	id := h.ID(h.Must("tag.add", `{"name":"billing"}`))
	res := h.Must("tag.edit", `{"id":"billing","globs":"services/billing/** db/billing/*.sql"}`)
	if !strings.Contains(txt(t, res), "services/billing/**,db/billing/*.sql") || h.ID(res) != id {
		t.Fatalf("edit by name = %q", txt(t, res))
	}
	res = h.Must("tag.edit", `{"id":"`+id+`","name":"payments","globs":"-"}`)
	if h.ID(res) != id || strings.Contains(txt(t, res), `"globs"`) || !strings.Contains(txt(t, res), "payments") {
		t.Fatalf("rename + clear = %q", txt(t, res))
	}
	h.Fails("tag.edit", `{"id":"payments"}`, errs.InvalidInput)
	h.Fails("tag.edit", `{"id":"nope","color":"#fff"}`, errs.NotFound)
	h.Fails("tag.edit", `{"id":"payments","name":"Bad Name"}`, errs.InvalidIdentifier)
}
