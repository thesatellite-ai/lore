package trash_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/ops/trash"
)

// TestTrashShowMasksResourceFields (DELETION 3.11, RQ1): a trashed
// resource's sensitive field values stay hidden in `lore trash show`
// (they live in the row's "fields" JSON, which the resources table
// definition does not describe); plain values show; --show-sensitive
// prints them.
func TestTrashShowMasksResourceFields(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"ip","type":"text","sensitive":true}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`)
	id := h.ID(h.MustYes(settings.OpResAdd, `{"kind":"host","name":"hub","set":["ip=203.0.113.10","os=debian-12"]}`))
	h.MustYes(settings.OpResDelete, `{"id":"hub"}`)
	res := h.Must(trash.OpShow, `{"id":"`+id+`"}`)
	out := opstest.Text(res) + h.JSON(res)
	if strings.Contains(out, "203.0.113.10") || !strings.Contains(out, "debian-12") {
		t.Fatalf("trash show: %s", out)
	}
	res = h.MustYes(trash.OpShow, `{"id":"`+id+`","show_sensitive":true}`)
	if !strings.Contains(opstest.Text(res)+h.JSON(res), "203.0.113.10") {
		t.Fatalf("trash show --show-sensitive: %s", h.JSON(res))
	}
}
