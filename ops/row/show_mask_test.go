package row_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/row"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestShowMasksSensitiveMeta pins METADATA 3.20 on `lore show <id>`: a
// metadata value is masked when the entry's label says sensitive, and when
// only the key row does (an entry written before the key turned sensitive),
// in text and --json; a plain key still shows its value.
func TestShowMasksSensitiveMeta(t *testing.T) {
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"vpn_host","type":"text","sensitive":true}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"later_secret","type":"text"}`)
	h.MustYes(settings.OpMetaAdd, `{"name":"client","type":"text"}`)
	id := h.ID(h.MustYes("memory.add", `{"body":"x","meta":["vpn_host=probe-entry-1","later_secret=probe-reg-2","client=acme"]}`))
	h.MustYes(settings.OpMetaEdit, `{"id":"later_secret","sensitive":true}`)
	res := h.Must(row.OpShow, `{"id":"`+id+`"}`)
	out := opstest.Text(res) + h.JSON(res)
	for _, leak := range []string{"probe-entry-1", "probe-reg-2"} {
		if strings.Contains(out, leak) {
			t.Errorf("show leaks %s: %s", leak, out)
		}
	}
	if !strings.Contains(out, "acme") || !strings.Contains(out, "•••") {
		t.Errorf("show output %s", out)
	}
}
