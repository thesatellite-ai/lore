package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

type clearIn struct {
	ID       string   `arg:"0" json:"id" desc:"the row"`
	Due      string   `flag:"due" json:"due,omitempty" desc:"due date"`
	ClearDue bool     `flag:"clear-due" json:"clear_due,omitempty" desc:"remove the due date"`
	NoMeta   bool     `flag:"no-meta" json:"no_meta,omitempty" desc:"do not copy metadata"`
	Meta     []string `flag:"meta" json:"meta,omitempty" desc:"metadata"`
}

type clearOut struct {
	ID string `json:"id"`
}

// clearFake is the fake registry plus a task.edit with a clearing pair.
func clearFake(t *testing.T) *fake {
	t.Helper()
	f := newFake(t)
	edit := op.Define(op.Spec{Name: "task.edit", Summary: "Edit a task", Permission: op.PermWrite, Access: op.AccessWrite},
		func(_ context.Context, in clearIn) (clearOut, error) { return clearOut{ID: in.ID}, nil })
	reg, err := op.NewRegistry(append(f.reg.Entries(), edit)...)
	if err != nil {
		t.Fatal(err)
	}
	f.reg = reg
	return f
}

// TestClearingSwitchWithItsFlag (devco:L-6): a clearing switch typed with
// the flag that sets the same field is a usage error naming both (it
// dropped --due silently); either one alone, the switch given =false,
// and --no-meta with --meta (they act on different things) still run.
func TestClearingSwitchWithItsFlag(t *testing.T) {
	f := clearFake(t)
	args := []string{"task", "edit", "tsk_a", "--clear-due", "--due", "2026-12-01"}
	r := execute(Build(f.reg, f, WithArgs(args)), "", args...)
	if r.exit != ExitUsage || !strings.Contains(r.stderr, "--clear-due and --due cannot be used together") || !strings.Contains(r.stderr, "hint:") {
		t.Errorf("%v: exit %d stderr %q", args, r.exit, r.stderr)
	}
	for _, args := range [][]string{
		{"task", "edit", "tsk_a", "--clear-due"},
		{"task", "edit", "tsk_a", "--due", "2026-12-01"},
		{"task", "edit", "tsk_a", "--clear-due=false", "--due", "2026-12-01"},
		{"task", "edit", "tsk_a", "--no-meta", "--meta", "a=b"},
	} {
		if r := execute(Build(f.reg, f, WithArgs(args)), "", args...); r.exit != 0 {
			t.Errorf("%v: exit %d stderr %q", args, r.exit, r.stderr)
		}
	}
}

// TestBadFlagValuePlainWords (devco:L-9): a value pflag's own parser
// refuses is named in plain words by the flag's type, never with the Go
// parser's text (strconv.ParseBool, strconv.ParseInt).
func TestBadFlagValuePlainWords(t *testing.T) {
	f := newFake(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"memory", "list", "--json=maybe"}, `--json takes true or false, not "maybe"`},
		{[]string{"memory", "add", "hello", "--force=perhaps"}, `--force takes true or false, not "perhaps"`},
	} {
		r := execute(Build(f.reg, f, WithArgs(c.args)), "", c.args...)
		if r.exit != ExitUsage || !strings.Contains(r.stderr+r.stdout, c.want) || strings.Contains(r.stderr+r.stdout, "strconv") {
			t.Errorf("%v: exit %d stderr %q stdout %q; want %q", c.args, r.exit, r.stderr, r.stdout, c.want)
		}
	}
}

// TestStrayWordAfterSwitchNamed (devco:L-4): the CLI hands the decoder the
// switch typed before a stray word, so the error names that switch.
func TestStrayWordAfterSwitchNamed(t *testing.T) {
	f := newFake(t)
	args := []string{"memory", "add", "hello", "--force", "extra"}
	r := execute(Build(f.reg, f, WithArgs(args)), "", args...)
	if r.exit == 0 || !strings.Contains(r.stderr, `unexpected value "extra" after --force (a switch takes no value)`) {
		t.Errorf("%v: exit %d stderr %q", args, r.exit, r.stderr)
	}
}
