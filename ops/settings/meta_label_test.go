package settings_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// labelFixture registers tier (low, high="High priority") and the
// sensitive zone (red="Red zone", blue) and writes one memory per case.
func labelFixture(t *testing.T) (*opstest.H, map[string]string) {
	t.Helper()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"tier","type":"text","values":"low,high"}`)
	h.MustYes(settings.OpMetaEdit, `{"id":"tier","value_label":["high=High priority"]}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"zone","type":"text","values":"red,blue","sensitive":true}`)
	h.MustYes(settings.OpMetaEdit, `{"id":"zone","value_label":["red=Red zone"]}`)
	rows := map[string]string{}
	for _, v := range []string{"high", "low"} {
		rows[v] = h.ID(h.MustYes(memAdd, `{"body":"tier `+v+`","meta":["tier=`+v+`","zone=red"]}`))
	}
	return h, rows
}

// TestMetaGetAllowedValueLabel (METADATA 3.17, G-META-10): meta get/show
// print a labelled allowed value by its label and an unlabelled one by its
// name; --json keeps the name in value and adds label only when there is
// one; a sensitive allowed value shows neither, until --show-sensitive.
func TestMetaGetAllowedValueLabel(t *testing.T) {
	t.Parallel()
	h, rows := labelFixture(t)
	for _, op := range []string{settings.OpMetaGet, settings.OpMetaShow} {
		for val, wantText := range map[string]string{"high": "tier\tHigh priority\n", "low": "tier\tlow\n"} {
			in := `{"id":"` + rows[val] + `"}`
			text := opstest.Text(h.Must(op, in))
			if !strings.Contains(text, wantText) || strings.Contains(text, "Red zone") || !strings.Contains(text, "zone\t•••\n") {
				t.Errorf("%s tier=%s text:\n%s", op, val, text)
			}
			var out settings.MetaGetOut
			if err := json.Unmarshal([]byte(h.JSON(h.Must(op, in))), &out); err != nil {
				t.Fatal(err)
			}
			for _, v := range out.Data {
				switch v.Key {
				case "tier":
					wantLabel := map[string]string{"high": "High priority", "low": ""}[val]
					if v.Value != val || v.Label != wantLabel {
						t.Errorf("%s tier=%s json %+v, want value %s label %q", op, val, v, val, wantLabel)
					}
				case "zone":
					if v.Label != "" || strings.Contains(v.Value, "red") {
						t.Errorf("%s sensitive zone json %+v", op, v)
					}
				}
			}
		}
	}
	revealed := opstest.Text(h.MustYes(settings.OpMetaGet, `{"id":"`+rows["high"]+`","show_sensitive":true}`))
	if !strings.Contains(revealed, "zone\tRed zone\n") {
		t.Errorf("--show-sensitive does not print the label:\n%s", revealed)
	}
}

// TestRowShowAllowedValueLabel (METADATA 3.17, G-META-10): lore show --id <id>
// prints a labelled allowed value by its label, an unlabelled one by its
// name, no "<key>_label" line; --json meta keeps the name and adds
// tier_label only for a labelled value; a sensitive allowed value shows
// neither.
func TestRowShowAllowedValueLabel(t *testing.T) {
	t.Parallel()
	h, rows := labelFixture(t)
	for val, want := range map[string]string{"high": "  meta tier: High priority\n", "low": "  meta tier: low\n"} {
		in := `{"id":"` + rows[val] + `"}`
		text := opstest.Text(h.Must("row.show", in))
		if !strings.Contains(text, want) || strings.Contains(text, "tier_label") || strings.Contains(text, "Red zone") || !strings.Contains(text, "  meta zone: •••\n") {
			t.Errorf("row show tier=%s text:\n%s", val, text)
		}
		var out struct {
			Meta map[string]string `json:"meta"`
		}
		raw := h.JSON(h.Must("row.show", in))
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		wantLabel := map[string]string{"high": "High priority", "low": ""}[val]
		if out.Meta["tier"] != val || out.Meta["tier_label"] != wantLabel {
			t.Errorf("row show tier=%s json meta %v", val, out.Meta)
		}
		if _, has := out.Meta["zone_label"]; has || strings.Contains(raw, "Red zone") {
			t.Errorf("sensitive label in json: %s", raw)
		}
	}
}
