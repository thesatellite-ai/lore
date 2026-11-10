package metalist

import (
	"testing"

	"github.com/thesatellite-ai/lore/engine"
)

// TestNoteLine: metadata notes print as is; every other W_ warning prints
// one "warning: <message> (<code>)" line, newlines folded, the code alone
// when there is no message; an empty metadata note and a non-W_ code print
// nothing.
func TestNoteLine(t *testing.T) {
	cases := []struct{ code, msg, want string }{
		{string(engine.WarnMeta), "new keys in repo: x (text)", "new keys in repo: x (text)\n"},
		{string(engine.WarnMeta), "", ""},
		{string(engine.WarnAuditDeferred), "the audit record of mem_1 is kept with the row", "warning: the audit record of mem_1 is kept with the row (W_AUDIT_DEFERRED)\n"},
		{"W_SCAN_DEGRADED", "scanner off", "warning: scanner off (W_SCAN_DEGRADED)\n"},
		{"W_SCAN_DEGRADED", "two\nlines  here\n", "warning: two lines here (W_SCAN_DEGRADED)\n"},
		{"W_PURGED_HISTORY", "", "warning: W_PURGED_HISTORY\n"},
		{"W_PURGED_HISTORY", " \n ", "warning: W_PURGED_HISTORY\n"},
		{"", "x", ""},
		{"E_INTERNAL", "not a warning", ""},
	}
	for _, c := range cases {
		if got := NoteLine(c.code, c.msg); got != c.want {
			t.Errorf("NoteLine(%q, %q) = %q, want %q", c.code, c.msg, got, c.want)
		}
	}
}
