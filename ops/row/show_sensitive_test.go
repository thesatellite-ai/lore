package row_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/row"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestShowSensitive (later 13, META 3.20, XQ3): `lore show <id>
// --show-sensitive` asks first (E_NEEDS_CONFIRM with no terminal and no
// --yes) and prints nothing; with --yes it unmasks sensitive metadata and
// meta_masked is empty. Without the flag the values are masked and
// meta_masked names each masked key, in --json and text.
func TestShowSensitive(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"vpn_host","type":"text","sensitive":true}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"vpn_pass_hint","type":"text","sensitive":true}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"client","type":"text"}`)
	id := h.ID(h.MustYes("memory.add", `{"body":"x","meta":["vpn_host=probe-host-1","vpn_pass_hint=probe-hint-2","client=acme"]}`))
	probes := []string{"probe-host-1", "probe-hint-2"}

	masked := showJSON(t, h, `{"id":"`+id+`"}`)
	if !slices.Equal(masked.MetaMasked, []string{"vpn_host", "vpn_pass_hint"}) || masked.Meta["client"] != "acme" {
		t.Fatalf("masked show: %+v", masked)
	}
	text := opstest.Text(h.Must(row.OpShow, `{"id":"`+id+`"}`))
	for _, p := range probes {
		if strings.Contains(text+h.JSON(h.Must(row.OpShow, `{"id":"`+id+`"}`)), p) {
			t.Fatalf("masked show leaks %s", p)
		}
	}

	msg := h.Fails(row.OpShow, `{"id":"`+id+`","show_sensitive":true}`, errs.NeedsConfirm)
	for _, p := range probes {
		if strings.Contains(msg, p) {
			t.Fatalf("the confirm refusal leaks %s: %s", p, msg)
		}
	}

	res := h.MustYes(row.OpShow, `{"id":"`+id+`","show_sensitive":true}`)
	var shown row.ShowOut
	if err := json.Unmarshal([]byte(h.JSON(res)), &shown); err != nil {
		t.Fatal(err)
	}
	if len(shown.MetaMasked) != 0 || shown.Meta["vpn_host"] != "probe-host-1" || shown.Meta["vpn_pass_hint"] != "probe-hint-2" {
		t.Fatalf("--show-sensitive --yes: %+v", shown)
	}
	if !strings.Contains(h.JSON(res), `"meta"`) || strings.Contains(h.JSON(res), `"meta_masked"`) {
		t.Fatalf("--show-sensitive json: %s", h.JSON(res))
	}
	if txt := opstest.Text(res); !strings.Contains(txt, "meta vpn_host: probe-host-1") {
		t.Fatalf("--show-sensitive text: %s", txt)
	}
}

// TestShowSensitiveResourceFields (later 13, RQ1): a resource row keeps a
// kind's values in its "fields" JSON, which the resources table definition
// does not describe; `lore show res_…` still hides each sensitive field's
// value (now and after an edit), keeps plain fields readable, names the
// hidden fields in meta_masked, and --show-sensitive --yes prints them.
func TestShowSensitiveResourceFields(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"ip","type":"text","sensitive":true}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"vpn","type":"text","sensitive":true}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`)
	id := h.ID(h.MustYes(settings.OpResAdd, `{"kind":"host","name":"hub","set":["ip=203.0.113.10","vpn=probe-vpn-7","os=debian-12"]}`))
	h.MustYes(settings.OpResEdit, `{"id":"hub","set":["ip=203.0.113.11"]}`)
	probes := []string{"203.0.113.10", "203.0.113.11", "probe-vpn-7"}
	in := `{"id":"` + id + `"}`

	out := showJSON(t, h, in)
	all := opstest.Text(h.Must(row.OpShow, in)) + h.JSON(h.Must(row.OpShow, in))
	for _, p := range probes {
		if strings.Contains(all, p) {
			t.Fatalf("show leaks %s:\n%s", p, all)
		}
	}
	if !slices.Equal(out.MetaMasked, []string{"ip", "vpn"}) || !strings.Contains(out.Fields["fields"], "debian-12") ||
		!strings.Contains(out.Fields["fields"], `"v":"(sensitive, hidden)"`) {
		t.Fatalf("masked resource show: %+v", out)
	}
	h.Fails(row.OpShow, `{"id":"`+id+`","show_sensitive":true}`, errs.NeedsConfirm)
	var shown row.ShowOut
	if err := json.Unmarshal([]byte(h.JSON(h.MustYes(row.OpShow, `{"id":"`+id+`","show_sensitive":true}`))), &shown); err != nil {
		t.Fatal(err)
	}
	if len(shown.MetaMasked) != 0 || !strings.Contains(shown.Fields["fields"], "203.0.113.11") || !strings.Contains(shown.Fields["fields"], "probe-vpn-7") {
		t.Fatalf("--show-sensitive resource show: %+v", shown)
	}
}
