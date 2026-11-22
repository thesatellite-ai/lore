package settings_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestMetaRulesRefuseActors pins devco D-885 (hunt H-144): people hold
// metadata, but a key cannot be required on (or defaulted onto) actors,
// whose rows lore mostly writes itself with no --meta; teams still can.
// A rule naming actors that an older lore stored stays inert (a person's
// first write is never refused for it) and meta lint names it with the
// edit that drops it, which runs as printed.
func TestMetaRulesRefuseActors(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"desk","type":"text"}`)
	for _, in := range []string{
		`{"id":"desk","required_on":"actors"}`,
		`{"id":"desk","required_on":"memories,actors"}`,
		`{"id":"desk","default":"4F","default_on":"actors"}`,
	} {
		msg := h.Fails(settings.OpMetaEdit, in, errs.InvalidInput)
		if !strings.Contains(msg, `"actors"`) || !strings.Contains(msg, "takes: ") || !strings.Contains(msg, "teams") || strings.Contains(msg, "actors, ") {
			t.Errorf("meta edit %s: the refusal names actors and the tables a rule takes (teams, not actors): %s", in, msg)
		}
	}
	h.MustYes(settings.OpMetaEdit, `{"id":"desk","required_on":"teams","default":"4F","default_on":"teams"}`)

	// a key stored by lore v0.1.29/v0.1.30 with required_on actors
	keyID, err := ids.ParseAny(h.ID(h.MustYes(settings.OpMetaAdd, `{"name":"pager","type":"text"}`)))
	if err != nil {
		t.Fatal(err)
	}
	rule, err := value.Parse(value.KindJSON, []byte(`["actors"]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.App.Update(context.Background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableMetaKeys, ID: keyID}, map[string]value.Value{"required_on": rule}); err != nil {
		t.Fatal(err)
	}
	h.Seed(schema.TableActors, nil) // a newcomer's actor row: not refused
	lint := opstest.Text(h.Must(settings.OpMetaLint, `{}`))
	fix := "lore meta edit --id pager --required-on none --yes"
	if !strings.Contains(lint, "required_on names a table that takes no create rule (actors)") || !strings.Contains(lint, fix) {
		t.Fatalf("meta lint does not name the inert rule and its fix %q:\n%s", fix, lint)
	}
	h.MustYes(settings.OpMetaEdit, `{"id":"pager","required_on":"none"}`)
	if lint = opstest.Text(h.Must(settings.OpMetaLint, `{}`)); strings.Contains(lint, "pager") {
		t.Fatalf("meta lint after the printed fix still names pager:\n%s", lint)
	}
}
