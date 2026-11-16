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

// TestResourceSizeWarningCountsValues (devco D-452): resources.warn_bytes
// measures the values the person set ({field: value} as JSON), not the
// row file, whose history repeats every value and whose field ids and
// envelope add more: about 600 characters of values read as 4365 bytes,
// and every edit grew it. 650 values stay under 700; 750 pass it, and the
// line says what it counted.
func TestResourceSizeWarningCountsValues(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"provider","type":"text"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"notes","type":"text"}`)
	h.MustYes(settings.OpPolicySet, setIn("resources.warn_bytes", "700", "repo"))
	add := func(name string, n int) string {
		in, _ := json.Marshal(map[string]any{"kind": "host", "name": name, "set": []string{"provider=hetzner", "notes=" + strings.Repeat("x", n)}})
		return opstest.Text(h.MustYes(settings.OpResAdd, string(in)))
	}
	if got := add("small", 600); strings.Contains(got, "warn_bytes") {
		t.Errorf("600 characters of values warned against 700 bytes:\n%s", got)
	}
	in, _ := json.Marshal(map[string]any{"id": "small", "set": []string{"notes=" + strings.Repeat("y", 600)}})
	if got := opstest.Text(h.MustYes(settings.OpResEdit, string(in))); strings.Contains(got, "warn_bytes") {
		t.Errorf("an edit to the same size warned (the history counted):\n%s", got)
	}
	got := add("big", 750)
	if !strings.Contains(got, "over resources.warn_bytes 700") || !strings.Contains(got, "values are 7") || !strings.Contains(got, "2 field value(s)") {
		t.Errorf("750 characters of values: want a warning naming the values' bytes:\n%s", got)
	}
}

// TestResourceHealthAgrees (devco D-450): resource stale and resource
// report give the same numbers in the same words ("2 to check: 0 stale
// (checked over 60 days ago), 2 never checked"); before, stale listed
// every never-checked row and report said "stale 0". report names where
// each secret reference is used and lists none under rotate outside an
// offboarding report; lint flags a reminder date that is empty or past.
func TestResourceHealthAgrees(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"domain"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"domain","field":"renews_on","type":"date","remind_before":"30d"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"domain","field":"registrar_login","type":"secret_ref"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"domain","name":"acme_com","set":["registrar_login=gopass://acme/registrar"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"domain","name":"acme_io","set":["renews_on=2020-01-01"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"domain","name":"acme_dev","set":["renews_on=2999-01-01"]}`)
	h.MustYes(settings.OpResCheck, `{"id":"acme_dev"}`)
	const summary = "2 to check: 0 stale (checked over 60 days ago), 2 never checked"
	stale := opstest.Text(h.Must(settings.OpResStale, `{}`))
	rep := opstest.Text(h.Must(settings.OpResReport, `{}`))
	for name, out := range map[string]string{"stale": stale, "report": rep} {
		if !strings.Contains(out, summary) {
			t.Errorf("resource %s lacks %q:\n%s", name, summary, out)
		}
	}
	if !strings.Contains(rep, "secret refs 1: gopass://acme/registrar (domain acme_com.registrar_login)") || strings.Contains(rep, "rotate ") {
		t.Errorf("report must name where the ref is used and rotate nothing outside offboarding:\n%s", rep)
	}
	var out settings.ResReportOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpResReport, `{"project":"repo"}`))), &out); err == nil && len(out.Rotate) > 0 && !strings.Contains(out.Rotate[0], "(domain acme_com.registrar_login)") {
		t.Errorf("offboarding rotate entries carry no reason: %v", out.Rotate)
	}
	lint := h.JSON(h.MustYes(settings.OpResLint, `{}`))
	for _, want := range []string{`"check":"remind_unset"`, `"name":"acme_com"`, `"check":"remind_passed"`, `"name":"acme_io"`} {
		if !strings.Contains(lint, want) {
			t.Errorf("lint lacks %s:\n%s", want, lint)
		}
	}
	if strings.Contains(lint, `"name":"acme_dev"`) {
		t.Errorf("a future date far from its reminder is a finding:\n%s", lint)
	}
}
