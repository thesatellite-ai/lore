package metalist

import (
	"testing"

	"github.com/thesatellite-ai/lore/engine"
)

func TestNoteLine(t *testing.T) {
	cases := []struct{ code, msg, want string }{
		{string(engine.WarnMeta), "new keys in repo: x (text)", "new keys in repo: x (text)\n"},
		{string(engine.WarnMeta), "", ""},
		{"W_SCAN_DEGRADED", "scanner off", ""},
		{"", "x", ""},
	}
	for _, c := range cases {
		if got := NoteLine(c.code, c.msg); got != c.want {
			t.Errorf("NoteLine(%q, %q) = %q, want %q", c.code, c.msg, got, c.want)
		}
	}
}
