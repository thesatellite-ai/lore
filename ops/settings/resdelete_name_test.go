package settings_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceDeleteUnknownNameIsNotFound: `resource delete <name>` looks
// the name up before it asks, so a name that does not exist is
// E_NOT_FOUND at once (no question about nothing), while a real name
// still asks.
func TestResourceDeleteUnknownNameIsNotFound(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"web1"}`)
	h.Fails(settings.OpResDelete, `{"id":"no_such_host"}`, errs.NotFound)
	h.Fails(settings.OpResDelete, `{"id":"web1"}`, errs.NeedsConfirm)
}
