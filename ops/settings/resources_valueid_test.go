package settings_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceValueIDsEndToEnd (later 33, OQ-AV): a closed field's values
// get val_ ids; input and --where take the name, the label (any case) or
// the val_ id (a --where label matches its exact spelling); a renamed
// value shows its new name in show and list and matches --where by the
// new name, label or the same id; the old name is refused
// (E_INVALID_INPUT); the value keeps its id across the rename.
func TestResourceValueIDsEndToEnd(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"env","type":"text","values":"prod,dev"}`)
	h.MustYes(settings.OpFieldEdit, `{"kind":"host","field":"env","value_labels":["prod=Production"]}`)
	idOf := func(name string) string {
		var kind settings.KindOut
		js := h.JSON(h.Must(settings.OpKindShow, `{"id":"host"}`))
		if err := json.Unmarshal([]byte(js), &kind); err != nil {
			t.Fatalf("%v: %s", err, js)
		}
		for _, f := range kind.Data.Fields {
			for _, v := range f.ValueIDs {
				if v.Name == name {
					return v.ID
				}
			}
		}
		t.Fatalf("no val_ id for %s in %s", name, js)
		return ""
	}
	prodID := idOf("prod")
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"a","set":["env=Production"]}`) // by label
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"b","set":["env=`+prodID+`"]}`) // by id
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"c","set":["env=dev"]}`)
	names := func(where string) string {
		res := h.Must(settings.OpResList, `{"kind":"host","where":[`+opstest.Quote(where)+`]}`)
		var out settings.ResListOut
		if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
			t.Fatal(err)
		}
		var ns []string
		for _, r := range out.Data {
			ns = append(ns, r.Name)
		}
		return strings.Join(ns, ",")
	}
	for where, want := range map[string]string{"env=prod": "a,b", "env=Production": "a,b", "env=" + prodID: "a,b", "env=dev": "c"} {
		if got := names(where); got != want {
			t.Errorf("before rename --where %s: %q, want %q", where, got, want)
		}
	}
	h.MustYes(settings.OpFieldEdit, `{"kind":"host","field":"env","rename_values":["prod=live"]}`)
	if id := idOf("live"); id != prodID {
		t.Fatalf("rename changed the id: %s -> %s", prodID, id)
	}
	for where, want := range map[string]string{"env=live": "a,b", "env=Production": "a,b", "env=" + prodID: "a,b"} {
		if got := names(where); got != want {
			t.Errorf("after rename --where %s: %q, want %q", where, got, want)
		}
	}
	// the old name is no longer an allowed value: refused, not an empty list
	h.Fails(settings.OpResList, `{"kind":"host","where":["env=prod"]}`, errs.InvalidInput)
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"a"}`)); !strings.Contains(s, "live") || strings.Contains(s, "env = prod") {
		t.Errorf("show after rename: %s", s)
	}
}
