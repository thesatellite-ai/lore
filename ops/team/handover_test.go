package team

import (
	"strings"
	"testing"
)

// TestStaffRemoveHandoverText pins B-12: member remove names the leaver's
// open tasks and the reassign line, by handle (or email without one), and
// no other member write prints a hand-over.
func TestStaffRemoveHandoverText(t *testing.T) {
	t.Parallel()
	m := StaffRow{ID: "act_1", Email: "ruth@example.com", Handle: "ruth"}
	out := StaffWriteOut{Member: m, Verb: "removed", Where: "acme org/", Handover: Handover{
		OpenTasks: []HandoverTask{{ID: "tsk_1", Title: "sync retry", Store: "repo"}}, Command: handoverCommand(m)}}
	got := out.Text()
	for _, want := range []string{"open tasks (1)", "tsk_1", "sync retry", "lore task reassign --from @ruth --assign @<someone> --yes"} {
		if !strings.Contains(got, want) {
			t.Errorf("member remove text lacks %q:\n%s", want, got)
		}
	}
	if c := handoverCommand(StaffRow{Email: "ruth@example.com"}); !strings.Contains(c, "--from ruth@example.com ") {
		t.Errorf("no handle: %q, want --from <email>", c)
	}
	if got := (StaffWriteOut{Member: m, Verb: "updated", Where: "acme org/"}).Text(); strings.Contains(got, "reassign") {
		t.Errorf("member edit printed a hand-over:\n%s", got)
	}
}
