package task_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestGroupMetaOnEveryRead (METADATA 3.17, MQ13, G-META-10): mission,
// plan and tasklist show print the metadata block (label of a labelled
// allowed value, a sensitive value masked and marked) and show/list
// --json carry meta, <key>_label and meta_masked; a row without metadata
// has no meta keys and no block.
func TestGroupMetaOnEveryRead(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	h.MustYes(settings.OpMetaAdd, `{"name":"tier","type":"text","values":"low,high"}`)
	h.MustYes(settings.OpMetaEdit, `{"id":"tier","value_label":["high=High priority"]}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"zone","type":"text","values":"red,blue","sensitive":true}`)
	nouns := []struct{ noun, add string }{
		{"mission", `{"title":"%s"%s}`},
		{"plan", `{"title":"%s","body":"b"%s}`},
		{"tasklist", `{"title":"%s","body":"b"%s}`},
	}
	for _, n := range nouns {
		t.Run(n.noun, func(t *testing.T) {
			mk := func(title, meta string) string {
				in := strings.Replace(strings.Replace(n.add, "%s", title, 1), "%s", meta, 1)
				return h.ID(h.MustYes(n.noun+".add", in))
			}
			with := mk(n.noun+" with", `,"meta":["tier=high","zone=red"]`)
			bare := mk(n.noun+" bare", "")
			text := opstest.Text(h.Must(n.noun+".show", `{"id":"`+with+`"}`))
			for _, want := range []string{"meta:\n", "  tier: High priority\n", "  zone: •••   (sensitive)\n"} {
				if !strings.Contains(text, want) {
					t.Errorf("show text lacks %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, "zone: red") {
				t.Errorf("show text leaks the sensitive value:\n%s", text)
			}
			if bt := opstest.Text(h.Must(n.noun+".show", `{"id":"`+bare+`"}`)); strings.Contains(bt, "meta:") {
				t.Errorf("a row without metadata prints a block:\n%s", bt)
			}
			want := map[string]string{"tier": "high", "tier_label": "High priority", "zone": "•••"}
			var show struct {
				Data struct {
					Meta       map[string]string `json:"meta"`
					MetaMasked []string          `json:"meta_masked"`
				} `json:"data"`
			}
			raw := h.JSON(h.Must(n.noun+".show", `{"id":"`+with+`"}`))
			if err := json.Unmarshal([]byte(raw), &show); err != nil {
				t.Fatal(err)
			}
			if !sameMap(show.Data.Meta, want) || strings.Join(show.Data.MetaMasked, ",") != "zone" || strings.Contains(raw, `"zone":"red"`) {
				t.Errorf("show json: %s", raw)
			}
			if braw := h.JSON(h.Must(n.noun+".show", `{"id":"`+bare+`"}`)); strings.Contains(braw, `"meta"`) {
				t.Errorf("bare show json has meta: %s", braw)
			}
			listIn := `{}`
			if n.noun == "mission" {
				listIn = `{"all":true}`
			}
			lraw := h.JSON(h.Must(n.noun+".list", listIn))
			var list struct {
				Data []struct {
					ID         string            `json:"id"`
					Meta       map[string]string `json:"meta"`
					MetaMasked []string          `json:"meta_masked"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(lraw), &list); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, d := range list.Data {
				if d.ID == with {
					found = sameMap(d.Meta, want) && strings.Join(d.MetaMasked, ",") == "zone"
				}
				if d.ID == bare && d.Meta != nil {
					t.Errorf("bare row in list has meta: %v", d.Meta)
				}
			}
			if !found || strings.Contains(lraw, `"zone":"red"`) {
				t.Errorf("list json: %s", lraw)
			}
		})
	}
}
