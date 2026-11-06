package settings_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestSettingsInTrash (DELETION 3.2): show, edit and delete of a trashed
// resource, metadata key or variable answer E_IN_TRASH (by name and by
// id); a name nobody ever used stays E_NOT_FOUND; meta show of a trashed
// row is E_IN_TRASH too.
func TestSettingsInTrash(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	res := h.ID(h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h1"}`))
	h.MustYes(settings.OpResDelete, `{"id":"h1"}`)
	h.Fails(settings.OpResShow, `{"id":"h1"}`, errs.InTrash)
	h.Fails(settings.OpResShow, `{"id":"`+res+`"}`, errs.InTrash)
	h.Fails(settings.OpResEdit, `{"id":"h1","set":["x=1"]}`, errs.InTrash)
	failsYes(t, h, settings.OpResDelete, `{"id":"h1"}`, errs.InTrash)
	h.Fails(settings.OpResShow, `{"id":"never"}`, errs.NotFound)
	h.Fails(settings.OpMetaShow, `{"id":"`+res+`"}`, errs.InTrash)

	h.MustYes(settings.OpMetaAdd, `{"name":"jira","type":"text"}`)
	h.MustYes(settings.OpMetaDelete, `{"id":"jira"}`)
	h.Fails(settings.OpMetaEdit, `{"id":"jira","strict":true}`, errs.InTrash)
	failsYes(t, h, settings.OpMetaDelete, `{"id":"jira"}`, errs.InTrash)

	h.MustYes(settings.OpVarsSet, `{"name":"region","value":"eu"}`)
	h.MustYes(settings.OpVarsRm, `{"id":"region"}`)
	h.Fails(settings.OpVarsShow, `{"id":"region"}`, errs.InTrash)
	failsYes(t, h, settings.OpVarsRm, `{"id":"region"}`, errs.InTrash)
	h.Fails(settings.OpVarsShow, `{"id":"never"}`, errs.NotFound)
}

// failsYes runs name with --yes and wants code (a confirm would answer first).
func failsYes(t *testing.T, h *opstest.H, name, input string, code errs.Code) {
	t.Helper()
	if _, err := h.RunYes(name, input); !errs.Has(err, code) {
		t.Fatalf("%s %s: %v, want %s", name, input, err, code)
	}
}
