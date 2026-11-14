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

// TestRosterIDInALiveRowIsTheProjection: a live-ref row file
// (<folder>/<table>/<id>.json, Item.LiveRow) names its author's actor id
// like any client row; the roster-id rule for text outside row files does
// not block it (a task added in a linked client repo was refused with
// "names a person on the roster"). The same text in a commit message
// still blocks.
func TestRosterIDInALiveRowIsTheProjection(t *testing.T) {
	idx := NewIndex([]IndexRow{{ID: actID, Table: "actors", Label: "store northwind/org"}}, nil)
	row := []byte(`{"_table":"tasks","id":"tsk_00000000000000000000000000000001","title":"fix totals","created_by":"` + actID + `"}`)
	if fs := Check([]Item{{Where: "live write _root/tasks/tsk_00000000000000000000000000000001.json", Path: "_root/tasks/tsk_00000000000000000000000000000001.json", Content: row, LiveRow: true}}, idx, Options{ClientRepo: true}); len(fs) != 0 {
		t.Fatalf("a live row naming its author was blocked: %+v", fs)
	}
	if fs := Check([]Item{{Where: "commit message", Content: row}}, idx, Options{ClientRepo: true}); len(fs) == 0 {
		t.Fatal("the same actor id in a commit message was not blocked")
	}
}

// TestRosterIDBesideItsProjectionIsNoLeak: a commit message naming a
// roster actor id is not refused when the same check carries that actor's
// row file (the projection the client repo receives with it): lore's own
// land commit lists the ids of the rows it lands, and its push was
// refused. Without the row file the id still blocks.
func TestRosterIDBesideItsProjectionIsNoLeak(t *testing.T) {
	idx := NewIndex([]IndexRow{{ID: actID, Table: "actors", Label: "store northwind/org"}}, nil)
	msg := Item{Where: "message of commit 1234567", Content: []byte("lore: land 2 rows (_root)\n\n" + actID + "\ntsk_00000000000000000000000000000001\n")}
	actor := Item{Where: ".lore/data/actors/" + actID + ".json", Path: ".lore/data/actors/" + actID + ".json", Content: []byte(`{"_table":"actors","id":"` + actID + `","display_name":"Alice","kind":"human"}`)}
	if fs := Check([]Item{msg, actor}, idx, Options{ClientRepo: true}); len(fs) != 0 {
		t.Fatalf("a land commit beside its actor copy was blocked: %+v", fs)
	}
	if fs := Check([]Item{msg}, idx, Options{ClientRepo: true}); len(fs) == 0 {
		t.Fatal("the actor id in a commit message without its projection was not blocked")
	}
}
