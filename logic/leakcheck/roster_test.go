package leakcheck

import "testing"

// TestRosterIDsAndEmailsBlockOutsideRowFiles (F-4): the org roster's actor
// ids and email addresses are internal details. A commit message or source
// file in a client repo naming them is refused like a rule or task id; a
// client row file may still carry the actor projection, and a git identity
// trailer (Signed-off-by:) names the commit's people by design.
func TestRosterIDsAndEmailsBlockOutsideRowFiles(t *testing.T) {
	idx := NewIndex([]IndexRow{
		{ID: actID, Table: "actors", Label: "store northwind/org", Emails: []string{"Alice@Northwind.example"}},
	}, nil)
	cases := []struct {
		name string
		item Item
		want bool // blocked
	}{
		{"actor id in a commit message", Item{Where: "commit message", Content: []byte("chore: ask " + actID)}, true},
		{"actor id in a source file", Item{Where: "src/a.ts", Path: "src/a.ts", Content: []byte("// owner " + actID)}, true},
		{"roster email in a commit message", Item{Where: "commit message", Content: []byte("chore: reviewed with Alice <alice@northwind.example>")}, true},
		{"roster email in a source file", Item{Where: "README.md", Path: "README.md", Content: []byte("contact ALICE@northwind.example")}, true},
		{"identity trailer", Item{Where: "commit message", Content: []byte("fix: x\n\nSigned-off-by: Alice <alice@northwind.example>\nCo-authored-by: Alice <alice@northwind.example>\n")}, false},
		{"another email", Item{Where: "commit message", Content: []byte("chore: ask bob@elsewhere.example")}, false},
		{"actor projection row file", Item{Where: "a", Path: ".lore/data/actors/" + actID + ".json", Content: []byte(`{"_table":"actors","id":"` + actID + `","display_name":"Alice","kind":"human"}`)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := Check([]Item{c.item}, idx, Options{ClientRepo: true})
			if got := len(fs) > 0; got != c.want {
				t.Fatalf("blocked = %v (%+v), want %v", got, fs, c.want)
			}
			for _, f := range fs {
				if f.Label != "store northwind/org" {
					t.Fatalf("finding %+v, want it labelled by the roster's store", f)
				}
			}
		})
	}
}
