package wire

import "testing"

// TestPushName (bug hunt W-14): a push is named by the folders whose rows
// it carried, not by the folder that happened to push.
func TestPushName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, own string
		folders   []string
		want      string
	}{
		{"acme/core", "core", nil, "acme/core"},
		{"acme/core", "core", []string{"core"}, "acme/core"},
		{"acme/core", "core", []string{"sync"}, "acme/sync"},
		{"acme/core", "core", []string{"core", "sync"}, "acme/core, acme/sync"},
		{"acme", ".", []string{"."}, "acme"},
		{"acme/org", "org", []string{"."}, "acme"},
	} {
		if got := pushName(c.name, c.own, c.folders); got != c.want {
			t.Errorf("pushName(%q, %q, %v) = %q, want %q", c.name, c.own, c.folders, got, c.want)
		}
	}
}
