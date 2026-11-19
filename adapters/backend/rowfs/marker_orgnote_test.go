package rowfs

import (
	"strings"
	"testing"
	"time"
)

// TestMarkerKeepsOrgNote (devco:D-721): org: and org_store: in
// lore-store.yaml name the org an org's other store belongs to and its main
// store; a marker rewrite (store set, compaction, the org's own writes)
// that dropped them would make a join by that store's url lose the org
// again.
func TestMarkerKeepsOrgNote(t *testing.T) {
	raw := "lore_store: 4\nid: sto_01a1a5aca7f57659be7320c5f4b70001\norg: acme\norg_store: git@github.com:acme-co/lore-team.git\n"
	m, err := ParseMarker([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if m.Org != "acme" || m.OrgStore != "git@github.com:acme-co/lore-team.git" {
		t.Fatalf("parsed org %q org_store %q", m.Org, m.OrgStore)
	}
	m.Epoch = 1
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"org: acme", "org_store: git@github.com:acme-co/lore-team.git"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("re-encoded marker lacks %q:\n%s", want, b)
		}
	}
	plain, _ := NewMarker(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if b, _ := plain.Encode(); strings.Contains(string(b), "org") {
		t.Errorf("a marker of no org names one:\n%s", b)
	}
}
