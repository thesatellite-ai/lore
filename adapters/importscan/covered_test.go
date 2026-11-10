package importscan

import (
	"path/filepath"
	"testing"
)

// One proposal per real source, never one equal to a setup that exists
// (enabled or in the trash), and a name-only collision is proposed under a
// free name that says why (polish item 3, realmac #20-22, m3, NEW-3).
func TestSoloProposalAgainstExisting(t *testing.T) {
	const other = "git@github-personal:someone/else.git"
	cases := []struct {
		name, body string
		want       string // the solo proposal's name, "" none
		note       string // its name field's note
	}{
		{"no setups: personal", "", "personal", ""},
		{"same identity, other store: covered (lore's own state)",
			"setups:\n  - name: mine\n    kind: solo\n    personal_store: " + other + "\n    identity:\n      key: HUMAN:PAT\n", "", ""},
		{"same store, other identity: covered",
			"setups:\n  - name: mine\n    kind: solo\n    personal_store: git@github-personal:octo-personal/notes.git\n    identity:\n      key: human:other@example.test\n", "", ""},
		{"inherit setup uses personal.store: covered",
			"setups:\n  - name: mine\n    kind: solo\n    identity:\n      key: human:other@example.test\n", "", ""},
		{"only the name collides: personal-2",
			"setups:\n  - name: personal\n    kind: solo\n    personal_store: " + other + "\n    identity:\n      key: human:other@example.test\n",
			"personal-2", "personal is taken by an existing setup"},
		{"name held by a trashed member: personal-2, says trash",
			"trash:\n  setups:\n    - name: personal\n      kind: member\n      org: elsewhere\n      removed_at: \"2026-11-06T10:00:00Z\"\n",
			"personal-2", "personal is taken by a setup in the trash"},
		{"same identity in the trash: covered",
			"trash:\n  setups:\n    - name: old\n      kind: solo\n      personal_store: " + other + "\n      identity:\n        key: human:pat\n      removed_at: \"2026-11-06T10:00:00Z\"\n", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			write(t, filepath.Join(w.lore, ConfigFile), configFull+tc.body)
			res := scan(t, w)
			var solo []Proposal
			for _, p := range res.Proposals {
				if p.Entry.Kind == "solo" {
					solo = append(solo, p)
				}
			}
			if tc.want == "" {
				if len(solo) != 0 || len(res.Covered) == 0 {
					t.Fatalf("solo proposals %+v, covered %v: want none proposed and a Covered line", solo, res.Covered)
				}
				return
			}
			if len(solo) != 1 || solo[0].Entry.Name != tc.want {
				t.Fatalf("solo proposals %+v, want exactly %s", solo, tc.want)
			}
			if got := solo[0].Fields[0]; got.Field != FieldName || got.Note != tc.note {
				t.Fatalf("name field %+v, want note %q", got, tc.note)
			}
		})
	}
}
