package wire

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStoreOwnNameInOutput pins B-9: a store keeps its own name in push
// and sync output whatever a repo calls it. In a client repo the org's
// name (`acme`) is the client store's linked folder; output names that
// store as `lore store list` names it from any repo, plus the folder, and
// adds the repo's name for it. The org file writes' normalised key gets
// the same name.
func TestStoreOwnNameInOutput(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfg := "orgs:\n  - name: acme\n    stores:\n" +
		"      - url: https://example.com/acme/team-store\n        org_folder: org\n" +
		"      - url: https://example.com/bigco/client-store\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	team, client := "https://example.com/acme/team-store", "https://example.com/bigco/client-store"
	for _, c := range []struct{ url, folder, want string }{
		{team, "", "acme"},
		{team, "org", "acme/org"},
		{client, "bigco_hub", "client-store/bigco_hub"},
		{"https://example.com/other/notes", "", ""},
	} {
		if got := storeOwnName(home, c.url, c.folder); got != c.want {
			t.Errorf("storeOwnName(%s, %q) = %q, want %q", c.url, c.folder, got, c.want)
		}
	}
	for _, c := range []struct{ name, own, want string }{
		{"acme", "client-store/bigco_hub", "client-store/bigco_hub (here: acme)"},
		{"acme/org", "acme/org", "acme/org"},
		{"acme/.", "acme", "acme/."},
		{"kb", "", "kb"},
	} {
		if got := storeShown(c.name, c.own); got != c.want {
			t.Errorf("storeShown(%q, %q) = %q, want %q", c.name, c.own, got, c.want)
		}
	}
	b := &storePush{name: "acme", own: "client-store/bigco_hub"}
	if got := b.pushShown("bigco_hub", []string{"bigco_hub"}); got != "client-store/bigco_hub (here: acme)" {
		t.Errorf("push line name %q", got)
	}
	if got := OrgStoreName(home, "example.com/bigco/client-store"); got != "client-store" {
		t.Errorf("OrgStoreName(normalised key) = %q, want client-store", got)
	}
}
