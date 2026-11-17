package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceListEveryKind (devco D-550): resource list with no --kind
// lists the resources of every kind (kind, then name), so `resource list
// --store <org>` answers like the rest of the reads; --where and --sort
// name a kind's fields and still need --kind.
func TestResourceListEveryKind(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"database"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"web1"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"database","name":"main_db"}`)
	got := opstest.Text(h.Must(settings.OpResList, `{}`))
	db, host := strings.Index(got, "main_db"), strings.Index(got, "web1")
	if db < 0 || host < 0 || db > host {
		t.Fatalf("resource list with no kind: want main_db (database) before web1 (host):\n%s", got)
	}
	if _, err := h.Run(settings.OpResList, `{"where":["a=b"]}`); !errs.Has(err, errs.InvalidInput) || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "--kind") {
		t.Fatalf("resource list --where with no kind: %v, want E_INVALID_INPUT naming --kind", err)
	}
}
