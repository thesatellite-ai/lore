package settings_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestResourcePolicyAndInputs: resources.allowed_in refuses a place it
// leaves out (RQ2, E_RESOURCE_PLACE), resources.warn_bytes prints a
// warning (RQ4), check and edit stamp checked_by (RQ8), and --set-json
// sets a list field (RES-OQ4).
func TestResourcePolicyAndInputs(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"provider","type":"text"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"tags","type":"list"}`)

	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"a","set_json":["tags=[\"x\",\"y\"]","provider=\"hetzner\""]}`)
	if got := opstest.Text(h.Must(settings.OpResShow, `{"id":"a"}`)); !strings.Contains(got, "tags = x, y") || !strings.Contains(got, "provider = hetzner") {
		t.Fatalf("set-json show %q", got)
	}
	h.MustYes(settings.OpResCheck, `{"id":"a"}`)
	if got := opstest.Text(h.Must(settings.OpResShow, `{"id":"a"}`)); !strings.Contains(got, "(by manual)") {
		t.Fatalf("check stamp %q", got)
	}
	h.MustYes(settings.OpResEdit, `{"id":"a","set":["provider=aws"]}`)
	if got := opstest.Text(h.Must(settings.OpResShow, `{"id":"a"}`)); !strings.Contains(got, "(by edit)") {
		t.Fatalf("edit stamp %q", got)
	}

	h.MustYes(settings.OpPolicySet, setIn("resources.warn_bytes", "10", "repo"))
	if got := opstest.Text(h.MustYes(settings.OpResAdd, `{"kind":"host","name":"b","set":["provider=hetzner"]}`)); !strings.Contains(got, "over resources.warn_bytes 10") {
		t.Fatalf("no size warning %q", got)
	}

	h.MustYes(settings.OpPolicySet, setIn("resources.allowed_in", "store", "repo"))
	if _, err := h.RunYes(settings.OpResAdd, `{"kind":"host","name":"c"}`); !errs.Has(err, errs.ResourcePlace) {
		t.Fatalf("allowed_in store, add to repo: %v, want E_RESOURCE_PLACE", err)
	}
}

// TestResourceConflictEntry: a strict merge clash leaves {"name",
// "strict", "conflict": {base, ours, theirs}} with no "v" (logic/merge,
// RESOURCES 3.13/R4): show lists it, an edit of another field keeps it,
// and --set on that field settles it with a plain {name, v}.
func TestResourceConflictEntry(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"provider","type":"text"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"region","type":"text"}`)
	var out settings.RowOut
	if err := json.Unmarshal([]byte(h.JSON(h.MustYes(settings.OpResAdd, `{"kind":"host","name":"a","set":["provider=aws","region=eu"]}`))), &out); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	src, r, err := h.App.Get(ctx, engine.Ref{Store: store.Source(out.Store), Table: schema.TableResources, ID: mustID(t, out.ID)})
	if err != nil {
		t.Fatal(err)
	}
	var entries map[string]json.RawMessage
	b, _ := value.JSONForm(r.Fields["fields"])
	if err = json.Unmarshal(b, &entries); err != nil {
		t.Fatal(err)
	}
	var provID string
	for id, e := range entries {
		if strings.Contains(string(e), `"provider"`) {
			provID = id
		}
	}
	entries[provID] = json.RawMessage(`{"name":"provider","strict":true,"conflict":{"base":"aws","ours":"gcp","theirs":"hetzner"}}`)
	raw, _ := json.Marshal(entries)
	jv, _ := value.JSONOf(raw)
	if _, err = h.App.Update(ctx, engine.Ref{Store: src, Table: schema.TableResources, ID: r.ID}, map[string]value.Value{"fields": jv}); err != nil {
		t.Fatal(err)
	}
	if got := opstest.Text(h.Must(settings.OpResShow, `{"id":"a"}`)); !strings.Contains(got, "provider CONFLICT  ours gcp | theirs hetzner | base aws") {
		t.Fatalf("conflict not shown %q", got)
	}
	h.MustYes(settings.OpResEdit, `{"id":"a","set":["region=us"]}`)
	if got := opstest.Text(h.Must(settings.OpResShow, `{"id":"a"}`)); !strings.Contains(got, "provider CONFLICT") || !strings.Contains(got, "region = us") {
		t.Fatalf("other edit dropped the conflict %q", got)
	}
	h.MustYes(settings.OpResEdit, `{"id":"a","set":["provider=gcp"]}`)
	if got := opstest.Text(h.Must(settings.OpResShow, `{"id":"a"}`)); strings.Contains(got, "CONFLICT") || !strings.Contains(got, "provider = gcp") {
		t.Fatalf("set did not settle %q", got)
	}
}
