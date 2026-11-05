package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceMaint: rename keeps refs, merge folds values and moves refs,
// lint finds missing required / deprecated / archived refs, retype checks
// every value first, and delete --with-dependents takes the impact set in
// one batch (RES 3.4, 3.6, 3.14, 3.23).
func TestResourceMaint(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"region","type":"text"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"service"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"runs_on","type":"ref","ref_kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"port","type":"text"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h1","set":["os=debian"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h1_dup","set":["region=eu"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"service","name":"api","set":["runs_on=h1_dup","port=8080"]}`)

	h.MustYes(settings.OpResRename, `{"id":"h1","new_name":"hub"}`)
	if _, err := h.RunYes(settings.OpResRename, `{"id":"hub","new_name":"Bad Name"}`); !errs.Has(err, errs.ResourceName) {
		t.Fatalf("bad rename: %v", err)
	}
	if _, err := h.RunYes(settings.OpResMerge, `{"id":"api","into":"hub"}`); !errs.Has(err, errs.ResourceKindMismatch) {
		t.Fatalf("merge across kinds: %v", err) // RES 3.6
	}
	got := opstest.Text(h.MustYes(settings.OpResMerge, `{"id":"h1_dup","into":"hub"}`))
	if !strings.Contains(got, "values filled from h1_dup: region") || !strings.Contains(got, "repointed 1 rows") {
		t.Fatalf("merge %q", got)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"api"}`)); !strings.Contains(s, "runs_on = hub") {
		t.Fatalf("after merge %q", s)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"hub"}`)); !strings.Contains(s, "region = eu") {
		t.Fatalf("survivor %q", s)
	}

	h.MustYes(settings.OpFieldEdit, `{"kind":"host","field":"os","deprecated":true}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"owner","type":"text","required":true,"default":"ops"}`)
	lint := opstest.Text(h.MustYes(settings.OpResLint, `{}`))
	if !strings.Contains(lint, "missing_required") || !strings.Contains(lint, "deprecated_field") {
		t.Fatalf("lint %q", lint)
	}

	if _, err := h.RunYes(settings.OpFieldRetype, `{"kind":"host","field":"os","type":"number"}`); !errs.Has(err, errs.ResourceType) {
		t.Fatalf("retype misfit: %v", err)
	}
	h.MustYes(settings.OpFieldRetype, `{"kind":"service","field":"port","type":"number"}`)
	if s := opstest.Text(h.Must(settings.OpKindShow, `{"id":"service"}`)); !strings.Contains(s, "port  number") {
		t.Fatalf("retyped %q", s)
	}

	if _, err := h.Run(settings.OpResDelete, `{"id":"hub","with_dependents":true}`); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("with-dependents must ask: %v", err)
	}
	del := opstest.Text(h.MustYes(settings.OpResDelete, `{"id":"hub","with_dependents":true,"reason":"gone"}`))
	if !strings.Contains(del, "2 resources (") || !strings.Contains(del, "moved to the trash") {
		t.Fatalf("delete %q", del)
	}
}

// TestResourceListMeta: resource list --meta filters by the row's
// metadata (CLI_REFERENCE 3.2).
func TestResourceListMeta(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"a1","meta":["client=acme"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"b1","meta":["client=globex"]}`)
	got := opstest.Text(h.Must(settings.OpResList, `{"kind":"host","meta_where":["client=acme"]}`))
	if !strings.Contains(got, "a1") || strings.Contains(got, "b1") {
		t.Fatalf("list --meta %q", got)
	}
}
