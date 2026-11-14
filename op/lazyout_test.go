package op

import (
	"context"
	"testing"
)

// TestDefineReadsOutputOnFirstUse: Define reads only the input's tags; an
// output tag is read (and a bad one panics) on the first OutputFields
// call (fastgate:G1a: startup no longer pays for every operation's output
// types).
func TestDefineReadsOutputOnFirstUse(t *testing.T) {
	t.Parallel()
	type badOut struct {
		A string // no json tag: a programmer error in an output type
	}
	o := Define(Spec{Name: "lazy.out"}, func(context.Context, struct{}) (badOut, error) { return badOut{}, nil })
	defer func() {
		if recover() == nil {
			t.Fatal("OutputFields of an untagged output field did not panic")
		}
	}()
	_ = o.OutputFields()
}
