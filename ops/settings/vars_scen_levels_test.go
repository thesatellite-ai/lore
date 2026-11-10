package settings_test

// User scenarios of variables across levels (vars where/list/show, CONFIG
// GUIDE "nearest definition per name"): the same name defined in the repo,
// a team store, its folder and its org folder resolves to the nearest one;
// where lists every definition, nearest first, the others hidden; removing
// the nearest uncovers the next; a set --to a place changes only that
// place's definition; an unknown place is refused and writes nothing.

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// varsScenDecode decodes a vars.list / vars.where result.
func varsScenDecode(t *testing.T, h *opstest.H, name, in string) settings.VarsOut {
	t.Helper()
	var out settings.VarsOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(name, in))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// varsScenShow is the value and place vars.show resolves name to.
func varsScenShow(t *testing.T, h *opstest.H, name string) (string, string) {
	t.Helper()
	var out struct {
		Data settings.Var `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpVarsShow, `{"id":`+opstest.Quote(name)+`}`))), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data.Value, out.Data.Store
}

func TestVarsScenNearestLevelWins(t *testing.T) {
	t.Parallel()
	h := multiStore(t, ownRepo)
	set := func(to, val string) {
		t.Helper()
		in := `{"name":"region","value":` + opstest.Quote(val)
		if to != "" {
			in += `,"to":` + opstest.Quote(to)
		}
		h.MustYes(settings.OpVarsSet, in+"}")
	}
	// Defined from the farthest to the nearest place.
	set("kb/org", "org-value")
	if v, st := varsScenShow(t, h, "region"); v != "org-value" || st != "kb/org" {
		t.Fatalf("only the org folder defines it: %s in %s", v, st)
	}
	set("kb", "store-value")
	set("", "repo-value")
	if v, st := varsScenShow(t, h, "region"); v != "repo-value" || st != "repo" {
		t.Fatalf("the repo is nearest: %s in %s", v, st)
	}
	w := varsScenDecode(t, h, settings.OpVarsWhere, `{"id":"region"}`)
	if w.Count != 3 || len(w.Data) != 3 {
		t.Fatalf("where lists every definition: %+v", w)
	}
	if w.Data[0].Store != "repo" || w.Data[0].Hidden || !w.Data[1].Hidden || !w.Data[2].Hidden {
		t.Fatalf("where: nearest first, the others hidden: %+v", w.Data)
	}
	l := varsScenDecode(t, h, settings.OpVarsList, `{}`)
	if l.Count != 1 || l.Data[0].Value != "repo-value" {
		t.Fatalf("list keeps the nearest definition per name: %+v", l)
	}
	// A set --to kb changes only that place.
	set("kb", "store-value-2")
	if v, _ := varsScenShow(t, h, "region"); v != "repo-value" {
		t.Fatalf("changing a farther place keeps the nearest value: %s", v)
	}
	// Removing the nearest uncovers the next.
	h.MustYes(settings.OpVarsRm, `{"id":"region"}`)
	if v, st := varsScenShow(t, h, "region"); v != "store-value-2" || st != "kb" {
		t.Fatalf("after rm in the repo: %s in %s", v, st)
	}
	// An unknown place is refused and writes nothing.
	if _, err := h.RunYes(settings.OpVarsSet, `{"name":"region","value":"x","to":"nosuch"}`); !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown --to: %v", err)
	}
	if w := varsScenDecode(t, h, settings.OpVarsWhere, `{"id":"region"}`); w.Count != 2 {
		t.Fatalf("a refused set added a definition: %+v", w)
	}
}

// A reference resolves through the nearest definition of each name, even
// when the referring variable lives farther out.
func TestVarsScenReferenceResolvesNearest(t *testing.T) {
	t.Parallel()
	h := multiStore(t, ownRepo)
	h.MustYes(settings.OpVarsSet, `{"name":"host","value":"org.example.com","to":"kb/org"}`)
	h.MustYes(settings.OpVarsSet, `{"name":"url","value":"https://${var:host}/x","to":"kb/org","type":"url"}`)
	h.MustYes(settings.OpVarsSet, `{"name":"host","value":"repo.example.com"}`)
	// A loop across places is still a loop.
	_, err := h.RunYes(settings.OpVarsSet, `{"name":"host","value":"${var:url}"}`)
	if !errs.Has(err, errs.RefCycle) {
		t.Fatalf("a loop across places: %v", err)
	}
	if v, _ := varsScenShow(t, h, "host"); v != "repo.example.com" {
		t.Fatalf("a refused loop changed the value: %s", v)
	}
}
