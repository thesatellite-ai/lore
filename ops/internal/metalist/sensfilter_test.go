package metalist_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestListSensitiveKeyFilterNote (METADATA 3.20, the class of later 395):
// a list whose --meta filter names a sensitive key (plain, scope-prefixed
// or by a dotted field) matches the stored value, prints it masked and
// adds "note: filtered on a sensitive key; values are masked"; a filter
// on a plain key or no filter adds no note.
func TestListSensitiveKeyFilterNote(t *testing.T) {
	h := opstest.New(t)
	h.MustYes("meta.add", `{"name":"vpn_host","type":"text","sensitive":true}`)
	h.MustYes("meta.add", `{"name":"client","type":"text"}`)
	h.MustYes("memory.add", `{"body":"a","meta":["vpn_host=vpn-7.internal","client=acme"]}`)
	h.MustYes("memory.add", `{"body":"b","meta":["vpn_host=vpn-8.internal","client=acme"]}`)
	const note = "note: filtered on a sensitive key; values are masked"
	cases := []struct {
		name, input string
		rows        int
		note        bool
	}{
		{"sensitive", `{"meta_where":["vpn_host=vpn-7.internal"]}`, 1, true},
		{"sensitive scoped", `{"meta_where":["repo:vpn_host=vpn-8.internal"]}`, 1, true},
		{"sensitive and plain", `{"meta_where":["client=acme","vpn_host~=vpn"]}`, 2, true},
		{"plain", `{"meta_where":["client=acme"]}`, 2, false},
		{"none", `{}`, 2, false},
	}
	for _, c := range cases {
		res := h.Must("memory.list", c.input)
		txt, js := opstest.Text(res), h.JSON(res)
		if strings.Contains(txt, note) != c.note {
			t.Errorf("%s: note=%v wanted:\n%s", c.name, c.note, txt)
		}
		if strings.Contains(txt+js, "vpn-7.internal") || strings.Contains(txt+js, "vpn-8.internal") {
			t.Errorf("%s: a sensitive value printed:\n%s", c.name, txt+js)
		}
		if n := strings.Count(js, `"id":"mem_`); n != c.rows {
			t.Errorf("%s: %d rows, want %d", c.name, n, c.rows)
		}
	}
}
