package metashow_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestAllowedValueLabel (METADATA 3.17, G-META-10): a show prints an
// allowed value's label in the text block when it has one and its name
// when it has none; --json keeps the name under the key and adds
// "<key>_label" only for a labelled value; a sensitive allowed value shows
// neither its name nor its label.
func TestAllowedValueLabel(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes("meta.add", `{"name":"tier","type":"text","values":"low,high"}`)
	h.MustYes("meta.edit", `{"id":"tier","value_label":["high=High priority"]}`)
	h.MustYes("meta.add", `{"name":"zone","type":"text","values":"red,blue","sensitive":true}`)
	h.MustYes("meta.edit", `{"id":"zone","value_label":["red=Red zone"]}`)
	cases := []struct {
		val, wantText, wantLabel string
	}{
		{"high", "  tier: High priority\n", "High priority"},
		{"low", "  tier: low\n", ""},
		{"High priority", "  tier: High priority\n", "High priority"}, // written by its label, stored by name
	}
	for _, c := range cases {
		id := h.ID(h.MustYes("memory.add", `{"body":"tiered","meta":[`+opstest.Quote("tier="+c.val)+`,"zone=red"]}`))
		text := opstest.Text(h.Must("memory.show", `{"id":"`+id+`"}`))
		if !strings.Contains(text, c.wantText) || strings.Contains(text, "tier_label") {
			t.Errorf("tier=%s: text block\n%s\nwant %q and no tier_label line", c.val, text, c.wantText)
		}
		if !strings.Contains(text, "  zone: •••   (sensitive)\n") || strings.Contains(text, "Red zone") || strings.Contains(text, "zone: red") {
			t.Errorf("sensitive allowed value leaks:\n%s", text)
		}
		var obj struct {
			Data struct {
				Meta map[string]string `json:"meta"`
			} `json:"data"`
		}
		raw := h.JSON(h.Must("memory.show", `{"id":"`+id+`"}`))
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatal(err)
		}
		m := obj.Data.Meta
		wantName := c.val
		if c.val == "High priority" {
			wantName = "high"
		}
		if m["tier"] != wantName || m["tier_label"] != c.wantLabel {
			t.Errorf("tier=%s: json meta %v, want tier=%s tier_label=%q", c.val, m, wantName, c.wantLabel)
		}
		if _, has := m["zone_label"]; has || strings.Contains(raw, "Red zone") {
			t.Errorf("sensitive label in json: %s", raw)
		}
	}
}
