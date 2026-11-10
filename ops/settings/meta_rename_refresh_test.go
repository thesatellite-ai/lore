package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaRenameRefreshOnNextWrite pins the lazy label refresh that
// `lore meta rename` and `meta edit --rename-value` promise (METADATA 3.8,
// 3.12): the registry changes alone; a later write of a row for another
// reason (a meta unset of another key, a body edit) rewrites that row's
// key name and allowed-value name from the registry by id; the key is
// then found by its new name; a write that changes nothing stays a no-op
// (no new hlc), and a row nobody wrote keeps its old labels.
func TestMetaRenameRefreshOnNextWrite(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"priority_band","type":"text","strict":true,"values":"low,normal,urgent"}`)
	a := h.ID(h.MustYes(memAdd, `{"body":"a","meta":["client=acme","priority_band=urgent","cost_hours=3"]}`))
	b := h.ID(h.MustYes(memAdd, `{"body":"b","meta":["client=globex","priority_band=urgent"]}`))
	c := h.ID(h.MustYes(memAdd, `{"body":"c","meta":["client=initech"]}`))

	h.MustYes(settings.OpMetaEdit, `{"id":"priority_band","rename_value":"urgent=asap"}`)
	h.MustYes(settings.OpMetaRename, `{"id":"client","new_name":"customer"}`)
	get := func(id string) string { return opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+id+`"}`)) }
	if got := get(a); !strings.Contains(got, "client\tacme") || !strings.Contains(got, "priority_band\turgent") {
		t.Fatalf("before any write the row keeps its labels:\n%s", got)
	}

	// a write for another reason refreshes the labels (the docs repro)
	h.MustYes(settings.OpMetaUnset, `{"id":"`+a+`","key":"cost_hours"}`)
	if got := get(a); got != "customer\tacme\npriority_band\tasap\n" {
		t.Fatalf("after meta unset of another key:\n%s", got)
	}
	// a plain row edit does too, and the key answers to its new name
	h.MustYes("memory.edit", `{"id":"`+b+`","body":"b2"}`)
	if got := get(b); got != "customer\tglobex\npriority_band\tasap\n" {
		t.Fatalf("after a body edit:\n%s", got)
	}
	h.MustYes(settings.OpMetaUnset, `{"id":"`+b+`","key":"customer"}`)
	if got := get(b); got != "priority_band\tasap\n" {
		t.Fatalf("unset by the new name:\n%s", got)
	}

	// a write that changes nothing is still a no-op: the labels wait
	before := getRow(t, h, c)
	h.MustYes("memory.edit", `{"id":"`+c+`","body":"c"}`)
	if after := getRow(t, h, c); after.HLC != before.HLC || !strings.Contains(get(c), "client\tinitech") {
		t.Fatalf("a no-op edit wrote the row: %v → %v\n%s", before.HLC, after.HLC, get(c))
	}
}
