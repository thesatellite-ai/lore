package cli

import (
	"strings"
	"testing"
)

// TestFlagWithoutValueBeforeAnotherFlag (devco:Q-7): `--id --body x` (an
// empty shell variable after --id) is a missing value for --id, never
// "--id is given twice"; a value that starts with "--" but names no flag of
// the command is still a value.
func TestFlagWithoutValueBeforeAnotherFlag(t *testing.T) {
	f := clearFake(t)
	args := []string{"task", "edit", "--id", "--due", "tomorrow"}
	r := execute(Build(f.reg, f, WithArgs(args)), "", args...)
	if r.exit == 0 || !strings.Contains(r.stderr, "missing value for --id: the next word is the flag --due") || strings.Contains(r.stderr, "given twice") {
		t.Errorf("%v: exit %d stderr %q", args, r.exit, r.stderr)
	}
	if !strings.Contains(r.stderr, "--id=--due") {
		t.Errorf("%v: the refusal names the = form: %q", args, r.stderr)
	}
	// devco:D-326: glued on with =, a value that is another flag's name is
	// the value (a title "--json" round trips through --title=--json).
	glued := []string{"task", "edit", "--id", "tsk_1", "--due=--id"}
	if r := execute(Build(f.reg, f, WithArgs(glued)), "", glued...); r.exit != 0 {
		t.Errorf("%v: a value glued with = is kept: exit %d stderr %q", glued, r.exit, r.stderr)
	}
	ok := []string{"task", "edit", "--id", "tsk_1", "--due", "--not-a-flag"}
	if r := execute(Build(f.reg, f, WithArgs(ok)), "", ok...); r.exit != 0 {
		t.Errorf("%v: a value that names no flag is kept: exit %d stderr %q", ok, r.exit, r.stderr)
	}
}
