package settings_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaShowSensitive (later 24, FA-META-3, METADATA 3.20): meta show,
// meta get and meta export mask a sensitive key (by its entry label, or
// by the key row alone) and name it in meta_masked (show/get --json);
// --show-sensitive asks first (E_NEEDS_CONFIRM without --yes, the
// refusal carries no value) and, with --yes, prints the values and
// meta_masked is gone.
func TestMetaShowSensitive(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"vpn_host","type":"text","sensitive":true}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"later_secret","type":"text"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"client","type":"text"}`)
	id := h.ID(h.MustYes(memAdd, `{"body":"x","meta":["vpn_host=probe-entry-1","later_secret=probe-reg-2","client=acme"]}`))
	h.MustYes(settings.OpMetaEdit, `{"id":"later_secret","sensitive":true}`)
	probes := []string{"probe-entry-1", "probe-reg-2"}

	for _, opName := range []string{settings.OpMetaShow, settings.OpMetaGet} {
		in := `{"id":"` + id + `"}`
		res := h.Must(opName, in)
		var out settings.MetaGetOut
		if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
			t.Fatal(err)
		}
		all := opstest.Text(res) + h.JSON(res)
		for _, p := range probes {
			if strings.Contains(all, p) {
				t.Fatalf("%s leaks %s: %s", opName, p, all)
			}
		}
		if !slices.Equal(out.Masked, []string{"later_secret", "vpn_host"}) || !strings.Contains(all, "acme") {
			t.Fatalf("%s masked: %+v", opName, out)
		}
		sens := `{"id":"` + id + `","show_sensitive":true}`
		msg := h.Fails(opName, sens, errs.NeedsConfirm)
		for _, p := range probes {
			if strings.Contains(msg, p) {
				t.Fatalf("%s refusal leaks %s", opName, p)
			}
		}
		shown := h.JSON(h.MustYes(opName, sens))
		if strings.Contains(shown, "meta_masked") || !strings.Contains(shown, "probe-entry-1") || !strings.Contains(shown, "probe-reg-2") {
			t.Fatalf("%s --show-sensitive: %s", opName, shown)
		}
	}

	exp := opstest.Text(h.Must(settings.OpMetaExport, `{}`))
	for _, p := range probes {
		if strings.Contains(exp, p) {
			t.Fatalf("export leaks %s: %s", p, exp)
		}
	}
	h.Fails(settings.OpMetaExport, `{"show_sensitive":true}`, errs.NeedsConfirm)
	if got := opstest.Text(h.MustYes(settings.OpMetaExport, `{"show_sensitive":true}`)); !strings.Contains(got, "probe-entry-1") || !strings.Contains(got, "probe-reg-2") {
		t.Fatalf("export --show-sensitive: %s", got)
	}
}
