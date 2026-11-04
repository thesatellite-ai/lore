package settings_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceBulkDelete: `resource delete --kind --where` (XQ1, XQ2):
// 25 matches over batch.preview_rows 20 ask, listing 20 and "5 more";
// --dry-run writes nothing; --yes deletes the 25 (not the non-matching
// row) under one bat_ batch whose log names the kind delete and the rows.
func TestResourceBulkDelete(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"provider","type":"text"}`)
	for i := range 25 {
		h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h`+strconv.Itoa(i)+`","set":["provider=linode"]}`)
	}
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"keep","set":["provider=hetzner"]}`)
	in := `{"kind":"host","where":["provider=linode"]}`
	_, err := h.Run(settings.OpResDelete, in)
	if !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(err.Error(), "25 rows, over batch.preview_rows 20") || !strings.Contains(err.Error(), "and 5 more") {
		t.Fatalf("want the preview, got %v", err)
	}
	dry := h.Must(settings.OpResDelete, `{"kind":"host","where":["provider=linode"],"dry_run":true}`)
	if !strings.Contains(opstest.Text(dry), "would delete 25 host resources") {
		t.Fatalf("dry run %q", opstest.Text(dry))
	}
	if n := countHosts(t, h); n != 26 {
		t.Fatalf("preview or dry run wrote: %d hosts left", n)
	}
	res := h.MustYes(settings.OpResDelete, in)
	var out settings.ResDeleteOut
	if err = json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Count != 25 || !strings.HasPrefix(out.Batch, "bat_") || !strings.Contains(opstest.Text(res), "25 host resources deleted (batch "+out.Batch+")") {
		t.Fatalf("out %+v %q", out, opstest.Text(res))
	}
	if n := countHosts(t, h); n != 1 {
		t.Fatalf("%d hosts left, want only keep", n)
	}
	evs, err := h.App.BatchEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, ev := range evs {
		if ev.Batch.String() == out.Batch {
			rows++
			if ev.Kind != "delete" || ev.Op != settings.OpResDelete {
				t.Fatalf("event %+v", ev)
			}
		}
	}
	if rows != 25 {
		t.Fatalf("batch log holds %d rows of %s", rows, out.Batch)
	}
	for name, bad := range map[string]string{
		"where without kind": `{"where":["provider=linode"]}`,
		"kind without where": `{"kind":"host"}`,
		"name and where":     `{"id":"keep","kind":"host","where":["provider=x"]}`,
		"no name":            `{}`,
		"unknown field":      `{"kind":"host","where":["colour=red"]}`,
	} {
		if _, err = h.RunYes(settings.OpResDelete, bad); !errs.Has(err, errs.InvalidInput) && !errs.Has(err, errs.ResourceInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// the one-row form still always asks.
	h.Fails(settings.OpResDelete, `{"id":"keep"}`, errs.NeedsConfirm)
}

func countHosts(t *testing.T, h *opstest.H) int {
	t.Helper()
	var l settings.ResListOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpResList, `{"kind":"host"}`))), &l); err != nil {
		t.Fatal(err)
	}
	return l.Count
}
