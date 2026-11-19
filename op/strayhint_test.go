package op

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// renameIn is shaped like team rename: a positional handle (also --handle)
// and a --to flag.
type renameIn struct {
	Handle string `arg:"0" flag:"handle" json:"handle,omitempty"`
	To     string `flag:"to" json:"to,omitempty"`
	Yes    bool   `flag:"yes" json:"yes,omitempty"`
}

// TestStrayValueHintNamesTheCommandsFlags (devco:T-2, T-6): a surplus word
// is answered with the command's own free flags and a quoting hint, never
// a made-up --name the command does not have.
func TestStrayValueHintNamesTheCommandsFlags(t *testing.T) {
	t.Parallel()
	o := Define(Spec{Name: "team.renamex", Summary: "s", Permission: PermRead, Access: AccessRead},
		func(_ context.Context, in renameIn) (renameIn, error) { return in, nil })
	_, err := o.Decode(ArgsDecoder{Args: []string{"platform", "backend"}, Flags: map[string][]string{"yes": {"true"}}})
	if !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("err = %v", err)
	}
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("not a typed error: %v", err)
	}
	h := e.Hint
	if strings.Contains(h, "--name") || !strings.Contains(h, `--to "value"`) || strings.Contains(h, "--handle") || !strings.Contains(h, "quote words") {
		t.Errorf("hint = %q", h)
	}
}
