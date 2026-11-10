package tagref

// Scenario: a tag created as "billing" and renamed to "payments" keeps its
// id (ids are permanent), which is the legacy id of "billing". The old
// name must stop matching it; a row written before per-store ids (legacy
// id, no live tag holds it) still matches by name.

import "testing"

func TestKnowledgeIndexHasAfterRename(t *testing.T) {
	const project = "prj_01a1b49ba460714bb910f88b4ce16605"
	billing, err := Legacy(project, "billing")
	if err != nil {
		t.Fatal(err)
	}
	old, err := Legacy(project, "old_only")
	if err != nil {
		t.Fatal(err)
	}
	idx := Index{project: {"payments": {billing.String()}}}
	have := []string{billing.String(), old.String()}
	cases := []struct {
		name string
		want bool
	}{
		{"payments", true},   // the live tag, by its new name
		{"billing", false},   // the old name of a renamed tag
		{"old_only", true},   // a legacy id no live tag holds
		{"unrelated", false}, // never bound
	}
	for _, c := range cases {
		if got := idx.Has(have, project, c.name); got != c.want {
			t.Errorf("Has(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
