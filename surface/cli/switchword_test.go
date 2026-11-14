package cli

import (
	"strings"
	"testing"
)

// TestSwitchWordRefused pins U-4 / the `--yes false` incident: pflag reads a
// switch with no `=value` as on and the word after it as a positional, so
// `--yes false` left the switch ON and fed "false" to the command. The word
// is refused (usage exit), the message names the switch and the word, and
// the hint gives the form that turns it on or off; `--force=false` and a
// switch before an ordinary value still work.
func TestSwitchWordRefused(t *testing.T) {
	f := newFake(t)
	for _, c := range []struct {
		args []string
		word string
	}{
		{[]string{"memory", "add", "hello", "--force", "false"}, "false"},
		{[]string{"memory", "add", "hello", "--yes", "no", "--force"}, "no"},
		{[]string{"memory", "add", "--force", "OFF", "hello"}, "OFF"},
	} {
		r := execute(Build(f.reg, f, WithArgs(c.args)), "", c.args...)
		if r.exit != ExitUsage || !strings.Contains(r.stderr, "is a switch and takes no value") || !strings.Contains(r.stderr, `"`+c.word+`" after it`) || !strings.Contains(r.stderr, "alone") {
			t.Errorf("%v: exit %d stderr %q stdout %q; want usage exit naming the switch and %q", c.args, r.exit, r.stderr, r.stdout, c.word)
		}
	}
	for _, args := range [][]string{
		{"memory", "add", "hello", "--force=false", "--yes", "--json"},
		{"memory", "add", "--force", "hello", "--yes", "--json"},
	} {
		if r := execute(Build(f.reg, f, WithArgs(args)), "", args...); r.exit != 0 {
			t.Errorf("%v: exit %d stderr %q; want it to run", args, r.exit, r.stderr)
		}
	}
}
