package incident_test

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/incident"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestRefResourceAmbiguous (RES 3.11, later 452): --ref takes a resource
// name; the same name under two kinds is E_RESOURCE_AMBIGUOUS on add and
// edit with nothing written, one name resolves, and add and edit declare
// the code.
func TestRefResourceAmbiguous(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"server"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"database"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"server","name":"hub_prod"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"server","name":"shared"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"database","name":"shared"}`)
	id := h.ID(h.Must(incident.OpAdd, `{"title":"t","body":"b","ref":["hub_prod"]}`))
	before := h.JSON(h.Must(incident.OpList, `{}`))
	h.Fails(incident.OpAdd, `{"title":"t","body":"b","ref":["shared"]}`, errs.ResourceAmbiguous)
	h.Fails(incident.OpEdit, `{"id":"`+id+`","ref":["shared"]}`, errs.ResourceAmbiguous)
	if h.JSON(h.Must(incident.OpList, `{}`)) != before {
		t.Fatal("a refused --ref wrote a row")
	}
	for _, name := range []string{incident.OpAdd, incident.OpEdit} {
		e, ok := h.App.Registry().Lookup(name)
		if !ok || !slices.Contains(e.Spec().Errors, errs.ResourceAmbiguous) {
			t.Errorf("%s does not declare E_RESOURCE_AMBIGUOUS", name)
		}
	}
}
