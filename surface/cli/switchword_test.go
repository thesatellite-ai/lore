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

// TestSwitchOffPair pins mac:named-no-pairs: every switch has a hidden
// --no-<switch> that turns it off (`--no-force`, `--no-force=false` turns
// it on), typing both spellings is a usage error naming both, a stray word
// after the off spelling is refused like after the switch, and the
// stray-word hint of the switch now names --no-<switch>.
func TestSwitchOffPair(t *testing.T) {
	f := newFake(t)
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"memory", "add", "hello", "--no-force", "--json"}, false},
		{[]string{"memory", "add", "hello", "--no-force=false", "--yes", "--json"}, true},
		{[]string{"memory", "add", "hello", "--force", "--yes", "--json"}, true},
		{[]string{"memory", "add", "hello", "--json"}, false},
	} {
		r := execute(Build(f.reg, f, WithArgs(c.args)), "", c.args...)
		if r.exit != 0 {
			t.Fatalf("%v: exit %d stderr %q", c.args, r.exit, r.stderr)
		}
		if got := strings.HasSuffix(echoOf(t, r), "|true"); got != c.want {
			t.Errorf("%v: force %v, want %v (echo %q)", c.args, got, c.want, echoOf(t, r))
		}
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"memory", "add", "hello", "--force", "--no-force"}, "--force and --no-force contradict each other"},
		{[]string{"memory", "add", "hello", "--no-force", "--force"}, "--force and --no-force contradict each other"},
		{[]string{"memory", "add", "hello", "--no-force", "false"}, "--no-force is a switch and takes no value"},
		{[]string{"memory", "add", "hello", "--force", "off"}, "to turn it off give --no-force"},
		{[]string{"memory", "add", "hello", "--yes", "false"}, "to turn it off give --no-yes"},
		{[]string{"memory", "add", "hello", "--yes", "--no-yes"}, "--yes and --no-yes contradict each other"},
	} {
		r := execute(Build(f.reg, f, WithArgs(c.args)), "", c.args...)
		if r.exit != ExitUsage || !strings.Contains(r.stderr, c.want) || r.stdout != "" {
			t.Errorf("%v: exit %d stderr %q stdout %q; want usage exit with %q", c.args, r.exit, r.stderr, r.stdout, c.want)
		}
	}
	// --json typed: the refusal is the JSON error object
	both := []string{"memory", "add", "hello", "--json", "--no-json"}
	if r := execute(Build(f.reg, f, WithArgs(both)), "", both...); r.exit != ExitUsage || !strings.Contains(r.stdout, "--json and --no-json contradict each other") {
		t.Errorf("%v: exit %d stdout %q stderr %q", both, r.exit, r.stdout, r.stderr)
	}
	// --no-yes leaves the confirmation to the person; --no-json prints text
	if r := execute(Build(f.reg, f, WithArgs([]string{"memory", "add", "hello", "--force", "--no-yes"})), "", "memory", "add", "hello", "--force", "--no-yes"); r.exit == 0 {
		t.Errorf("--force --no-yes ran without the confirmation: %q", r.stdout)
	}
	if r := execute(Build(f.reg, f), "", "memory", "add", "hello", "--no-json"); r.exit != 0 || strings.HasPrefix(strings.TrimSpace(r.stdout), "{") {
		t.Errorf("--no-json: exit %d stdout %q stderr %q", r.exit, r.stdout, r.stderr)
	}
	// hidden: --help lists the switch, not its off spelling
	if r := execute(Build(f.reg, f), "", "memory", "add", "--help"); !strings.Contains(r.stdout, "--force") || strings.Contains(r.stdout, "--no-force") {
		t.Errorf("help:\n%s", r.stdout)
	}
}
