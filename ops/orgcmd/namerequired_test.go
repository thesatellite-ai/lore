package orgcmd

import (
	"testing"
)

// TestNamingVerbsNeverTakeTheDefaultOrg pins plain-files decision D-ai: refresh,
// link and unlink fall back to the only (or the active setup's) org, but
// create, rename and set name a new entry, a renamed one or the committed
// .lore/org.yaml, so they keep requiring the name even on a host that has a
// default org: a guess there would make, rename or commit the wrong entry.
// Each refuses before the host writes anything.
func TestNamingVerbsNeverTakeTheDefaultOrg(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body string }{
		{OpCreate, `{"store":"s","key":"human:m@a.example","name":"Mira"}`},
		{OpRename, `{"new":"b"}`},
		{OpSet, `{"commit":true}`},
		{OpImport, `{}`},
	} {
		h := &defaultingHost{org: "acme", why: "the only org entry on this machine"}
		if _, err := run(t, h, tc.name, tc.body, true); err == nil {
			t.Errorf("%s %s: ran without a name (default org taken?)", tc.name, tc.body)
		}
		if h.writes != 0 {
			t.Errorf("%s %s: the host wrote %d times", tc.name, tc.body, h.writes)
		}
	}
	h := &defaultingHost{org: "acme", why: "the only org entry on this machine"}
	if _, err := run(t, h, OpLink, `{"project":"p"}`, true); err != nil || h.writes != 1 {
		t.Fatalf("link with a default org: %v, writes %d (the defaulting verbs must keep working)", err, h.writes)
	}
}
