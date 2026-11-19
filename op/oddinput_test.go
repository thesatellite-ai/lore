package op

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// oddIn has a positional title (also --title), a switch and a duration.
type oddIn struct {
	Title   string        `arg:"0" flag:"title" json:"title,omitempty"`
	DryRun  bool          `flag:"dry-run" json:"dry_run,omitempty"`
	Timebox time.Duration `flag:"timebox" json:"timebox,omitempty"`
}

func oddOp() *Op[oddIn, oddIn] {
	return Define(Spec{Name: "task.odd", Summary: "s", Permission: PermRead, Access: AccessRead},
		func(_ context.Context, in oddIn) (oddIn, error) { return in, nil })
}

// TestDecodeStrayWordAfterSwitch (devco:L-4): a word typed right after a
// switch is named as such, never blamed on --title ("given twice"), and
// a surplus word with no switch before it keeps the old wording.
func TestDecodeStrayWordAfterSwitch(t *testing.T) {
	t.Parallel()
	o := oddOp()
	_, err := o.Decode(ArgsDecoder{Args: []string{"stray"}, Flags: map[string][]string{"title": {"p"}, "dry-run": {"true"}}, AfterSwitch: map[string]string{"stray": "dry-run"}})
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), `unexpected value "stray" after --dry-run (a switch takes no value)`) || strings.Contains(err.Error(), "given twice") {
		t.Errorf("after a switch: err = %v", err)
	}
	_, err = o.Decode(ArgsDecoder{Args: []string{"stray"}, Flags: map[string][]string{"title": {"p"}}})
	if err == nil || !strings.Contains(err.Error(), "--title is given twice") {
		t.Errorf("no switch before it: err = %v", err)
	}
	if _, err = o.Decode(ArgsDecoder{Args: []string{"p"}, Flags: map[string][]string{"dry-run": {"true"}}, AfterSwitch: map[string]string{"p": "dry-run"}}); err != nil {
		t.Errorf("a positional the command takes, after a switch: %v", err)
	}
}

// TestDecodeRefusesNonPositiveDuration (devco:L-7): a duration at or
// below zero is refused naming the flag; a positive one decodes.
func TestDecodeRefusesNonPositiveDuration(t *testing.T) {
	t.Parallel()
	o := oddOp()
	for _, v := range []string{"-5m", "0s", "0"} {
		_, err := o.Decode(ArgsDecoder{Flags: map[string][]string{"title": {"p"}, "timebox": {v}}})
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "--timebox must be more than 0") {
			t.Errorf("--timebox %s: err = %v", v, err)
		}
	}
	if _, err := o.Decode(ArgsDecoder{Flags: map[string][]string{"title": {"p"}, "timebox": {"45m"}}}); err != nil {
		t.Errorf("--timebox 45m: %v", err)
	}
}
