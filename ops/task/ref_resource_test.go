package task_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestRefResourceName (RES 3.11, later 452): task add and edit take a
// resource name as --ref: one live resource of that name becomes a
// resources pointer; the same name under two kinds is
// E_RESOURCE_AMBIGUOUS and writes nothing; add and edit declare that code.
func TestRefResourceName(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"server"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"database"}`)
	hub := h.ID(h.MustYes(settings.OpResAdd, `{"kind":"server","name":"hub_prod"}`))
	h.MustYes(settings.OpResAdd, `{"kind":"server","name":"shared"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"database","name":"shared"}`)

	id := add(h, "rotate certs", `"ref":["hub_prod"]`)
	if js := h.JSON(h.Must(opstask.OpShow, `{"id":"`+id+`"}`)); !strings.Contains(js, hub) {
		t.Fatalf("add --ref hub_prod does not point at %s:\n%s", hub, js)
	}
	_, err := h.Run(opstask.OpAdd, `{"title":"ambiguous","ref":["shared"]}`)
	if !errs.Has(err, errs.ResourceAmbiguous) {
		t.Fatalf("add --ref shared: want E_RESOURCE_AMBIGUOUS, got %v", err)
	}
	if js := h.JSON(h.Must(opstask.OpList, `{}`)); strings.Contains(js, "ambiguous") {
		t.Fatalf("a refused add wrote a task:\n%s", js)
	}
	other := add(h, "plain", "")
	_, err = h.Run(opstask.OpEdit, `{"id":"`+other+`","ref":["shared"]}`)
	if !errs.Has(err, errs.ResourceAmbiguous) {
		t.Fatalf("edit --ref shared: want E_RESOURCE_AMBIGUOUS, got %v", err)
	}
	for _, name := range []string{opstask.OpAdd, opstask.OpEdit} {
		e, ok := h.App.Registry().Lookup(name)
		if !ok || !slices.Contains(e.Spec().Errors, errs.ResourceAmbiguous) {
			t.Errorf("%s does not declare E_RESOURCE_AMBIGUOUS", name)
		}
	}
}
