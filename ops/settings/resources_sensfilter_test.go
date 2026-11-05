package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceListSensitiveFilterNote (later 395, RES 3.12): --where on a
// sensitive field matches the stored value, prints it masked and adds the
// "filtered on a sensitive field" note (text and --json notes); a filter
// on a plain field, no filter, and --show-sensitive --yes add no note.
func TestResourceListSensitiveFilterNote(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"ip","type":"text","sensitive":true}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"hub_prod","set":["ip=203.0.113.10","os=debian"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"hub_stag","set":["ip=198.51.100.7","os=debian"]}`)
	const note = "note: filtered on a sensitive field; values are masked"
	cases := []struct {
		name, input string
		yes         bool
		rows        []string
		note        bool
	}{
		{"sensitive filter", `{"kind":"host","where":["ip~=203.0.113"]}`, false, []string{"hub_prod"}, true},
		{"sensitive and plain", `{"kind":"host","where":["os=debian","ip=198.51.100.7"]}`, false, []string{"hub_stag"}, true},
		{"plain filter", `{"kind":"host","where":["os=debian"]}`, false, []string{"hub_prod", "hub_stag"}, false},
		{"no filter", `{"kind":"host"}`, false, []string{"hub_prod", "hub_stag"}, false},
		{"show sensitive", `{"kind":"host","where":["ip~=203.0.113"],"show_sensitive":true}`, true, []string{"hub_prod"}, false},
	}
	for _, c := range cases {
		var res op.Result
		if c.yes {
			res = h.MustYes(settings.OpResList, c.input)
		} else {
			res = h.Must(settings.OpResList, c.input)
		}
		txt, js := opstest.Text(res), h.JSON(res)
		for _, r := range c.rows {
			if !strings.Contains(txt, r) {
				t.Errorf("%s: row %s missing:\n%s", c.name, r, txt)
			}
		}
		if got := strings.Count(txt, "\n") - 1; got != len(c.rows)+boolN(c.note) { // header + rows + note
			t.Errorf("%s: %d lines after the header:\n%s", c.name, got, txt)
		}
		if strings.Contains(txt, note) != c.note || strings.Contains(js, note) != c.note {
			t.Errorf("%s: note=%v wanted\n%s\n%s", c.name, c.note, txt, js)
		}
		if !c.yes && (strings.Contains(txt+js, "203.0.113.10") || strings.Contains(txt+js, "198.51.100.7")) {
			t.Errorf("%s: a sensitive value printed:\n%s", c.name, txt+js)
		}
	}
}

func boolN(b bool) int {
	if b {
		return 1
	}
	return 0
}
