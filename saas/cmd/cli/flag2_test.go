package main

import (
	"strings"
	"testing"
)

// flag2Exit is the exit of every input error (FLAG2: class bad_input).
const flag2Exit = 20

// TestFlag2InputErrorsExit20 walks the ways an argument can be missing or
// wrong, on old (cobra-built) and generated commands alike: each exits 20
// with E_INVALID_INPUT printed once (FLAG2; v0.1.17 and cobra exited 1).
func TestFlag2InputErrorsExit20(t *testing.T) {
	f := newCoreFixture(t)
	cases := []struct {
		name string
		args []string
		say  string
	}{
		{"unknown root command", []string{"nosuchverb"}, `unknown command "nosuchverb"`},
		{"unknown subcommand of a generated group", []string{"memory", "nosuchverb"}, `unknown command "nosuchverb"`},
		{"unknown flag on an old command", []string{"version", "--nope"}, "unknown flag: --nope"},
		{"unknown flag on a generated command", []string{"memory", "list", "--nope"}, "unknown flag: --nope"},
		{"unknown shorthand flag", []string{"version", "-Z"}, "unknown shorthand flag"},
		// org link is the operation org.link since P3-ORG-G-2: the op's
		// decoder names the missing positional and the extra arguments.
		{"missing positional on an operation", []string{"org", "link"}, "missing required field org"},
		{"too many arguments on an operation", []string{"org", "link", "a", "b"}, "too many arguments: want at most 1"},
		{"required flag not set", []string{"org", "link", "northwind"}, "--project <folder>"},
		{"missing id on a generated show", []string{"memory", "show"}, "missing required field id"},
	}
	for _, c := range cases {
		r := f.run(e2eBin, c.args...)
		if r.code != flag2Exit || strings.Count(r.stderr, "[E_INVALID_INPUT]") != 1 || !strings.Contains(r.stderr, c.say) {
			t.Errorf("%s: lore %v: exit %d, want %d with one [E_INVALID_INPUT] and %q:\n%s", c.name, c.args, r.code, flag2Exit, c.say, r.stderr)
		}
	}
}

// TestActorDeleteRefused: `lore actor delete` is refused with E_REFUSED
// (exit 23) naming `lore actor deactivate` (BATCH2 #8: actors are never
// deleted, DELETION 4.1); before, it printed the group help with exit 0.
func TestActorDeleteRefused(t *testing.T) {
	f := newCoreFixture(t)
	for _, args := range [][]string{{"actor", "delete", "act_0123456789abcdef0123456789abcdef"}, {"actor", "delete"}} {
		r := f.run(e2eBin, args...)
		if r.code != 23 || !strings.Contains(r.stderr, "[E_REFUSED]") || !strings.Contains(r.stderr, "lore actor deactivate") {
			t.Errorf("lore %v: exit %d, want 23 with E_REFUSED naming lore actor deactivate:\n%s", args, r.code, r.stderr)
		}
	}
}
