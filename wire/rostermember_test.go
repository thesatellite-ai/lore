package wire

import (
	"strings"
	"testing"
)

// TestParseMemberRef pins B-12's references: a handle, an email, and the
// stray @ before an email (`--from @ruth@example.com`) name a member; an
// actor id, a stable key or a display name do not (the caller resolves
// those its own way).
func TestParseMemberRef(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want memberRef
		ok   bool
	}{
		{"@ana", memberRef{handle: "ana"}, true},
		{" @ana ", memberRef{handle: "ana"}, true},
		{"Ana@Example.com", memberRef{email: "ana@example.com"}, true},
		{"@ana@example.com", memberRef{email: "ana@example.com"}, true},
		{"act_0123", memberRef{}, false},
		{"human:ana@example.com", memberRef{}, false},
		{"Ana Able", memberRef{}, false},
		{"@", memberRef{}, false},
	}
	for _, c := range cases {
		got, ok := parseMemberRef(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseMemberRef(%q) = %+v %v, want %+v %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestMemberRefMatchesLeftMember pins that the match ignores status (a
// member who left is still found; rosterMember prefers an active one) and
// that an email matches the stable key's name part in any case.
func TestMemberRefMatchesLeftMember(t *testing.T) {
	t.Parallel()
	gone := RosterActor{ID: "act_1", Handle: "ruth", StableKey: "human:ruth@example.com", Active: false, Status: "left"}
	for _, ref := range []string{"@ruth", "ruth@example.com", "RUTH@example.com", "@ruth@example.com"} {
		m, ok := parseMemberRef(ref)
		if !ok || !m.matches(gone) {
			t.Errorf("%q does not match the member who left", ref)
		}
	}
	if m, _ := parseMemberRef("@ruthie"); m.matches(gone) {
		t.Error("@ruthie matched @ruth")
	}
	if got := memberMissErr(newRosterAt("acme/org", "acme org/"), "nobody@example.com").Error(); got == "" || strings.Contains(got, "@nobody@") {
		t.Errorf("miss text %q names the email with a stray @", got)
	}
}
