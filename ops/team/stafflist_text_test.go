package team

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/textwidth"
)

// TestStaffListAlignsWideAndLongNames pins devco:N-2: member list columns
// line up in terminal cells for CJK, accented and long names (each column
// as wide as its widest cell), and the handle and teams are columns too.
func TestStaffListAlignsWideAndLongNames(t *testing.T) {
	t.Parallel()
	out := StaffListOut{Where: "acme", Members: []StaffRow{
		{ID: "act_1", Email: "li@example.test", Name: "李小龙", Role: "member", Handle: "li", Teams: []string{"web"}},
		{ID: "act_2", Email: "ana@example.test", Name: "Ana-María de la Peña", Role: "member", Handle: "anamaria", Teams: []string{"web"}},
		{ID: "act_3", Email: "ngozi@example.test", Name: "Ngozi Okonkwo-Adeyemi", Role: "admin", Handle: "ngozi", Teams: []string{"api"}},
		{ID: "act_4", Email: "bo@example.test", Name: "Bo", Handle: "bo", Status: "left"},
	}}.Text()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 lines, got:\n%s", out)
	}
	// The role, handle and teams columns start at the same cell on every row.
	cols := map[string][]string{
		"role":   {"  member", "  member", "  admin", "  -  "},
		"handle": {"  @li ", "  @anamaria ", "  @ngozi ", "  @bo "},
	}
	for name, probes := range cols {
		want := -1
		for i, l := range lines {
			at := textwidth.Width(l[:strings.Index(l, probes[i])])
			if want < 0 {
				want = at
			} else if at != want {
				t.Fatalf("%s column starts at cell %d and %d:\n%s", name, want, at, out)
			}
		}
	}
	teams := -1
	for _, l := range lines[:3] {
		at := textwidth.Width(l[:strings.Index(l, "teams @")])
		if teams < 0 {
			teams = at
		} else if at != teams {
			t.Fatalf("teams column starts at cell %d and %d:\n%s", teams, at, out)
		}
	}
	if !strings.Contains(out, "Ngozi Okonkwo-Adeyemi  admin") {
		t.Fatalf("a long name runs into the role column:\n%s", out)
	}
}
