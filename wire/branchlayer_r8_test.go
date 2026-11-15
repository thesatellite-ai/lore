package wire

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestEnvLayerAcceptsEveryCase (devco:V-4): LORE_LAYER=OFF and
// LORE_LAYER=0 were ignored without a word (an exact compare with "off"),
// so the row went into the branch layer. off/0/false in any case turn
// layers off, on/1/true and unset leave them on, anything else is
// E_INVALID_INPUT naming the accepted values and LORE_LAYER=off.
func TestEnvLayerAcceptsEveryCase(t *testing.T) {
	for _, tc := range []struct {
		v       string
		off, ok bool
	}{
		{"", false, true}, {"off", true, true}, {"OFF", true, true}, {"Off", true, true}, {"0", true, true},
		{"false", true, true}, {"FALSE", true, true}, {" off ", true, true},
		{"on", false, true}, {"ON", false, true}, {"1", false, true}, {"true", false, true},
		{"nope", false, false}, {"no", false, false}, {"offf", false, false},
	} {
		t.Run(tc.v, func(t *testing.T) {
			t.Setenv(EnvLayer, tc.v)
			off, err := envLayerSetting()
			if tc.ok != (err == nil) || off != tc.off {
				t.Fatalf("LORE_LAYER=%q: off %v err %v, want off %v ok %v", tc.v, off, err, tc.off, tc.ok)
			}
			if err != nil {
				var e *errs.Error
				if !errors.As(err, &e) || e.Code != errs.InvalidInput || !strings.Contains(e.Message, "off, 0, false") || !strings.Contains(e.Hint, "LORE_LAYER=off") {
					t.Fatalf("refusal: %v", err)
				}
			}
			if envLayerIsOff() != (tc.off && tc.ok) {
				t.Fatalf("envLayerIsOff disagrees for %q", tc.v)
			}
		})
	}
}

// TestPromoteBadIDNamesCauseAndNext (devco:V-6): `lore branch promote
// --ids mem_doesnotexist` said only "[E_INVALID_ID] --ids
// mem_doesnotexist". It now says why the value is no row id, what one
// looks like, and that `lore branch show --branch <b>` lists the ids.
func TestPromoteBadIDNamesCauseAndNext(t *testing.T) {
	_, err := parseIDs([]string{"mem_doesnotexist"}, "hotfix/1")
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.InvalidID {
		t.Fatalf("err = %v, want E_INVALID_ID", err)
	}
	for _, want := range []string{"mem_doesnotexist", "is not a row id", "32 hex digits"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("message %q lacks %q", e.Message, want)
		}
	}
	if !strings.Contains(e.Hint, "lore branch show --branch hotfix/1") {
		t.Errorf("hint %q does not name lore branch show --branch hotfix/1", e.Hint)
	}
	if _, err = parseIDs([]string{"bad"}, ""); err == nil || !strings.Contains(err.Error(), "is not a row id") {
		t.Fatalf("no branch known: %v", err)
	}
	if got, err := parseIDs([]string{"mem_0193f2a7c4b77e1d8a3f5b6c7d8e9f00, rul_0193f2a7c4b77e1d8a3f5b6c7d8e9f01"}, "x"); err != nil || len(got) != 2 {
		t.Fatalf("good ids: %v %v", got, err)
	}
}

// TestLayerCacheExplainedInHomeLayout (devco:V-17): the lore home README
// said nothing of _layers/, while every code branch read made a folder
// there. Its cache/stores row now names the layer cache path, and a file
// under it is explained by the layout as a branch's cache.
func TestLayerCacheExplainedInHomeLayout(t *testing.T) {
	e, ok := matchHome("cache/stores", true)
	if !ok || !strings.Contains(e.What, layerCacheSeg+"/<repo key>/<branch key>/") || !strings.Contains(e.What, "lore cache prune") {
		t.Fatalf("cache/stores row: %+v", e)
	}
	f, ok := matchHome("cache/stores/sto_0123/main/"+layerCacheSeg+"/2b6e06283b0d/feat%2Fx/app.db", false)
	if !ok || !strings.Contains(f.What, "code branch") {
		t.Fatalf("a layer cache file: %+v %v", f, ok)
	}
}
