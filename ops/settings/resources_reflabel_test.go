package settings_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
)

// refLabelOf is the stored v_label of resource id's field (by name).
func refLabelOf(t *testing.T, h *opstest.H, id, field string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(filepath.Join(h.Repo, ".lore", "data", schema.TableResources, id+".json")))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Fields map[string]struct {
			Name  string `json:"name"`
			Label string `json:"v_label"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, e := range doc.Fields {
		if e.Name == field {
			return e.Label
		}
	}
	return "<no field " + field + ">"
}

// rowID is the id of a RowOut's JSON.
func rowID(t *testing.T, h *opstest.H, out op.Result) string {
	t.Helper()
	var r settings.RowOut
	if err := json.Unmarshal([]byte(h.JSON(out)), &r); err != nil {
		t.Fatal(err)
	}
	return r.ID
}

// TestResourceRenameRewriteRows (RES 3.4, OQ17, PASS3-A-3, P3-RES-1): a ref
// stores its target's "<kind>/<name>" label beside the id; a rename alone
// leaves referrers' labels until their next write (and says how many), and
// --rewrite-rows rewrites every referrer now in one batch, touching no row
// that refs another resource.
func TestResourceRenameRewriteRows(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpKindAdd, `{"kind":"service"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"runs_on","type":"ref","ref_kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"service","field":"port","type":"text"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h1"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h2"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"lonely"}`)
	a := rowID(t, h, h.MustYes(settings.OpResAdd, `{"kind":"service","name":"a","set":["runs_on=h1"]}`))
	b := rowID(t, h, h.MustYes(settings.OpResAdd, `{"kind":"service","name":"b","set":["runs_on=h1"]}`))
	c := rowID(t, h, h.MustYes(settings.OpResAdd, `{"kind":"service","name":"c","set":["runs_on=h2"]}`))
	if got := refLabelOf(t, h, a, "runs_on"); got != "host/h1" {
		t.Fatalf("label on add = %q, want host/h1", got)
	}

	// No flag: referrers keep the old label, the count is reported.
	got := opstest.Text(h.MustYes(settings.OpResRename, `{"id":"h1","new_name":"hub"}`))
	if !strings.Contains(got, "2 ref labels refresh on each row's next write") {
		t.Fatalf("rename without --rewrite-rows: %q", got)
	}
	if l := refLabelOf(t, h, a, "runs_on"); l != "host/h1" {
		t.Fatalf("rename alone rewrote a referrer: %q", l)
	}
	// The next real write of a referrer refreshes its label.
	h.MustYes(settings.OpResEdit, `{"id":"a","set":["port=80"]}`)
	if l := refLabelOf(t, h, a, "runs_on"); l != "host/hub" {
		t.Fatalf("label after edit = %q, want host/hub", l)
	}

	// --rewrite-rows: both referrers now, one batch; c (refs h2) untouched.
	out := h.MustYes(settings.OpResRename, `{"id":"hub","new_name":"hub2","rewrite_rows":true}`)
	got = opstest.Text(out)
	if !strings.Contains(got, "rewrote 2 ref labels (batch bat_") {
		t.Fatalf("rename --rewrite-rows: %q", got)
	}
	var ro settings.RowOut
	if err := json.Unmarshal([]byte(h.JSON(out)), &ro); err != nil || !strings.HasPrefix(ro.Batch, "bat_") {
		t.Fatalf("batch in JSON %q (%v)", h.JSON(out), err)
	}
	for _, id := range []string{a, b} {
		if l := refLabelOf(t, h, id, "runs_on"); l != "host/hub2" {
			t.Fatalf("%s label = %q, want host/hub2", id, l)
		}
	}
	if l := refLabelOf(t, h, c, "runs_on"); l != "host/h2" {
		t.Fatalf("unrelated referrer changed: %q", l)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"a"}`)); !strings.Contains(s, "runs_on = hub2") {
		t.Fatalf("show after rewrite %q", s)
	}

	// Run twice: nothing left to rewrite once labels match.
	got = opstest.Text(h.MustYes(settings.OpResRename, `{"id":"lonely","new_name":"alone","rewrite_rows":true}`))
	if !strings.Contains(got, "no ref labels to rewrite") {
		t.Fatalf("rename of an unreferenced resource: %q", got)
	}
}
