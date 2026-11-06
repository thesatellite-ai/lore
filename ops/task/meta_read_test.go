package task_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestTaskMetaOnEveryRead (METADATA 3.17 "metadata on every read", MQ13,
// G-META-10): task show prints a "meta:" block (a labelled allowed value by
// its label, an unlabelled one by its name, a sensitive value masked and
// marked), and --json carries meta with <key>_label beside a labelled
// value and meta_masked naming the masked key, never the sensitive value
// or its label; task list --json rows carry the same; a task without
// metadata prints and serialises exactly as before (no meta keys).
func TestTaskMetaOnEveryRead(t *testing.T) {
	h := opstest.New(t, repoClient(false))
	h.MustYes(settings.OpMetaAdd, `{"name":"tier","type":"text","values":"low,high"}`)
	h.MustYes(settings.OpMetaEdit, `{"id":"tier","value_label":["high=High priority"]}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"zone","type":"text","values":"red,blue","sensitive":true}`)
	h.MustYes(settings.OpMetaEdit, `{"id":"zone","value_label":["red=Red zone"]}`)
	high := add(h, "tier high", `"meta":["tier=high","zone=red","owner_note=plain text"]`)
	low := add(h, "tier low", `"meta":["tier=low"]`)
	bare := add(h, "no metadata", "")

	type metaOut struct {
		Meta       map[string]string `json:"meta"`
		MetaMasked []string          `json:"meta_masked"`
	}
	cases := []struct {
		id        string
		text      []string
		notInText []string
		meta      map[string]string
		masked    []string
	}{
		{high,
			[]string{"meta:\n", "  tier: High priority\n", "  zone: •••   (sensitive)\n", "  owner_note: plain text\n"},
			[]string{"Red zone", "zone: red", "tier_label", "zone_label"},
			map[string]string{"tier": "high", "tier_label": "High priority", "zone": "•••", "owner_note": "plain text"},
			[]string{"zone"}},
		{low, []string{"meta:\n", "  tier: low\n"}, []string{"tier_label", "zone"},
			map[string]string{"tier": "low"}, nil},
		{bare, nil, []string{"meta:"}, nil, nil},
	}
	for _, c := range cases {
		in := `{"id":"` + c.id + `"}`
		text := opstest.Text(h.Must(opstask.OpShow, in))
		for _, want := range c.text {
			if !strings.Contains(text, want) {
				t.Errorf("%s show text lacks %q:\n%s", c.id, want, text)
			}
		}
		for _, bad := range c.notInText {
			if strings.Contains(text, bad) {
				t.Errorf("%s show text has %q:\n%s", c.id, bad, text)
			}
		}
		raw := h.JSON(h.Must(opstask.OpShow, in))
		var out struct {
			Data metaOut `json:"data"`
		}
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		if !sameMap(out.Data.Meta, c.meta) || strings.Join(out.Data.MetaMasked, ",") != strings.Join(c.masked, ",") {
			t.Errorf("%s show json meta %v masked %v; want %v %v", c.id, out.Data.Meta, out.Data.MetaMasked, c.meta, c.masked)
		}
		if strings.Contains(raw, "Red zone") || strings.Contains(raw, `"zone":"red"`) {
			t.Errorf("%s show json leaks the sensitive value: %s", c.id, raw)
		}
		if c.meta == nil && (strings.Contains(raw, `"meta"`) || strings.Contains(raw, `"meta_masked"`)) {
			t.Errorf("a task without metadata gained meta keys: %s", raw)
		}
	}

	raw := h.JSON(h.Must(opstask.OpList, `{}`))
	var list struct {
		Data []struct {
			ID string `json:"id"`
			metaOut
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "Red zone") || strings.Contains(raw, `"zone":"red"`) {
		t.Errorf("list json leaks the sensitive value: %s", raw)
	}
	seen := 0
	for _, d := range list.Data {
		for _, c := range cases {
			if d.ID != c.id {
				continue
			}
			seen++
			if !sameMap(d.Meta, c.meta) || strings.Join(d.MetaMasked, ",") != strings.Join(c.masked, ",") {
				t.Errorf("list %s meta %v masked %v; want %v %v", d.ID, d.Meta, d.MetaMasked, c.meta, c.masked)
			}
		}
	}
	if seen != len(cases) {
		t.Fatalf("list showed %d of %d tasks: %s", seen, len(cases), raw)
	}
}

// sameMap compares two string maps (nil equals empty).
func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
