package team

import (
	"strings"
	"testing"
)

// TestTeamListColumnsFitLongNames guards devco D-572: a team name longer
// than the old fixed 24 columns ran into the member count, and the list
// printed no count.
func TestTeamListColumnsFitLongNames(t *testing.T) {
	t.Parallel()
	out := TeamListOut{Where: "acme", Teams: []Team{
		{Handle: "admins", Name: "Lore admins (company store owners)", Active: true, Members: make([]TeamMember, 1)},
		{Handle: "web", Name: "Web", Active: true, Members: make([]TeamMember, 3)},
	}}.Text()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 || lines[2] != "2 teams" {
		t.Fatalf("want 2 rows and a count line, got:\n%s", out)
	}
	if !strings.Contains(lines[0], "Lore admins (company store owners)  1 member(s)") {
		t.Fatalf("the name runs into the count:\n%s", out)
	}
	if strings.Index(lines[0], "member(s)") != strings.Index(lines[1], "member(s)") {
		t.Fatalf("the count column is not aligned:\n%s", out)
	}
}
