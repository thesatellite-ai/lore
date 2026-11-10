package settings_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaSetEcho (later 516, OV10/OV56): meta set echoes "set key =
// value" read back from the saved row: a plain key its value, a
// sensitive key "= •••", a dotted field of an object key that field's
// value (later 516), a list key its items.
func TestMetaSetEcho(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"client","type":"text"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"vpn_host","type":"text","sensitive":true}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"deploy","type":"json"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"pages","type":"list"}`)
	id := h.ID(h.MustYes("memory.add", `{"body":"x","meta_json":["deploy={\"env\":\"stag\",\"n\":2}"]}`))
	cases := []struct {
		name, input, want string
	}{
		{"plain", `{"id":"` + id + `","pair":"client=acme"}`, "set client = acme"},
		{"sensitive", `{"id":"` + id + `","pair":"vpn_host=vpn-7-probe"}`, "set vpn_host = •••"},
		{"dotted field", `{"id":"` + id + `","pair":"deploy.env=prod"}`, "set deploy.env = prod"},
		{"list", `{"id":"` + id + `","json":["pages=[\"a\",\"b\"]"]}`, "set pages = a | b"},
	}
	for _, c := range cases {
		got := opstest.Text(h.MustYes(settings.OpMetaSet, c.input))
		if !strings.Contains(got, c.want) || strings.Contains(got, "vpn-7-probe") {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestEditOnlyMetaEveryNoun (later 516, OV56): `<noun> edit <id> --meta
// k=v` with no other flag is a real change on every noun: it writes the
// value (meta get reads it back) instead of answering "nothing to change".
func TestEditOnlyMetaEveryNoun(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"client","type":"text"}`)
	nouns := []struct{ noun, add string }{
		{"memory", `{"body":"m"}`},
		{"rule", `{"text":"always r"}`},
		{"decision", `{"title":"d","body":"b"}`},
		{"hotfix", `{"title":"h","body":"b"}`},
		{"pattern", `{"title":"p","body":"b"}`},
		{"task", `{"title":"t"}`},
	}
	for _, n := range nouns {
		id := h.ID(h.MustYes(n.noun+".add", n.add))
		h.MustYes(n.noun+".edit", `{"id":"`+id+`","meta":["client=acme-`+n.noun+`"]}`)
		if got := opstest.Text(h.Must(settings.OpMetaGet, `{"id":"`+id+`"}`)); got != "client\tacme-"+n.noun+"\n" {
			t.Errorf("%s edit --meta only: meta get %q", n.noun, got)
		}
	}
}
