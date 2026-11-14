package op

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// staleIn has one min-tagged flag whose zero value means "use the default"
// and one plain int flag, so the test sees the tag change only its field.
type staleIn struct {
	Days  int `flag:"days" min:"1" json:"days,omitempty"`
	Other int `flag:"other" json:"other,omitempty"`
}

func staleOp() *Op[staleIn, staleIn] {
	return Define(Spec{Name: "task.stale", Summary: "s", Permission: PermRead, Access: AccessRead},
		func(_ context.Context, in staleIn) (staleIn, error) { return in, nil })
}

// TestMinFlag (G-20): `--days 0` is refused naming the allowed range,
// instead of decoding as "not given" and silently running with the
// default. An absent flag, a value at or over the floor and a plain int
// flag's 0 decode as before.
func TestMinFlag(t *testing.T) {
	t.Parallel()
	o := staleOp()
	for _, v := range []string{"0", "-3"} {
		_, err := o.Decode(ArgsDecoder{Flags: map[string][]string{"days": {v}}})
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "--days must be 1 or more") {
			t.Errorf("--days %s: err = %v", v, err)
		}
	}
	for name, d := range map[string]Decoder{
		"absent":     ArgsDecoder{},
		"one":        ArgsDecoder{Flags: map[string][]string{"days": {"1"}}},
		"plain zero": ArgsDecoder{Flags: map[string][]string{"other": {"0"}}},
	} {
		if _, err := o.Decode(d); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestMinTagMisuse: min goes on an int flag and is an integer; anything
// else is a Define-time panic, like every other bad tag.
func TestMinTagMisuse(t *testing.T) {
	t.Parallel()
	type badStr struct {
		Days string `flag:"days" min:"1" json:"days,omitempty"`
	}
	type badNum struct {
		Days int `flag:"days" min:"x" json:"days,omitempty"`
	}
	for name, def := range map[string]func(){
		"string": func() {
			Define(Spec{Name: "a.b", Summary: "s", Permission: PermRead, Access: AccessRead},
				func(_ context.Context, in badStr) (badStr, error) { return in, nil })
		},
		"not a number": func() {
			Define(Spec{Name: "a.b", Summary: "s", Permission: PermRead, Access: AccessRead},
				func(_ context.Context, in badNum) (badNum, error) { return in, nil })
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			def()
		}()
	}
}
