package renderhost

import (
	"strings"
	"testing"
)

// fileFor is the planned private file of the store whose id is id
// ("<store>-<id>.md", B-17); the test fails when no store has that id.
func (p *privatePlan) fileFor(t *testing.T, id string) string {
	t.Helper()
	for _, s := range p.stores {
		if s.stem == id || strings.HasSuffix(s.stem, "-"+id) {
			return s.stem + privateFileExt
		}
	}
	t.Fatalf("no private store with id %s in %+v", id, p.stores)
	return ""
}

// TestPrivateNamesSayWhich (B-17): render folders and files start with the
// repo's and the store's name and end with the permanent id; lore's own
// files are told apart from a person's by that id.
func TestPrivateNamesSayWhich(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ readable, id, want string }{
		{"auth", "rep_01ab", "auth-rep_01ab"},
		{"acme/org", "sto_01cd", "acme-org-sto_01cd"},
		{"Client Hub!", "sto_01cd", "client-hub-sto_01cd"},
		{"me", "sto_01ef", "me-sto_01ef"},
		{"", "sto_01ef", "sto_01ef"},
		{"rep_01ab", "rep_01ab", "rep_01ab"},
	} {
		if got := privateName(c.readable, c.id); got != c.want {
			t.Errorf("privateName(%q, %q) = %q, want %q", c.readable, c.id, got, c.want)
		}
	}
	for name, own := range map[string]bool{"index.md": true, "me-sto_01ef.md": true, "sto_01ef.md": true, "name-old.md": true, "notes.md": false, "me-sto_01ef.txt": false} {
		if privateOwnFile(name) != own {
			t.Errorf("privateOwnFile(%q) = %v, want %v", name, !own, own)
		}
	}
	idx := buildPrivateIndex("auth", "rep_01ab", []privateStore{{stem: "me-sto_1", sources: nil}, {stem: "acme-org-sto_2"}})
	if !strings.Contains(idx, "\n- me-sto_1.md  store ") || !strings.Contains(idx, "\n- acme-org-sto_2.md  store ") {
		t.Errorf("index stores are not bullets:\n%s", idx)
	}
}
