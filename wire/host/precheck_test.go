package host

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// precheckIn is the input of the test operation: one required argument
// and one flag the operation's own check refuses.
type precheckIn struct {
	Name string `json:"name" arg:"0" desc:"a name"`
	Bad  string `json:"bad,omitempty" flag:"bad" desc:"refused when set"`
}

// countingDecoder counts the decodes the Runner asks for.
type countingDecoder struct {
	op.ArgsDecoder
	n *int
}

// Decode implements op.Decoder.
func (d countingDecoder) Decode(dst reflect.Value, fields []op.FieldInfo) error {
	*d.n++
	return d.ArgsDecoder.Decode(dst, fields)
}

func precheckOp(name string, check bool) op.Entry {
	o := op.Define(op.Spec{Name: name, Summary: "probe", Permission: op.PermWrite, Access: op.AccessWrite},
		func(context.Context, precheckIn) (struct{}, error) { return struct{}{}, nil })
	if check {
		o.Checks(func(in precheckIn) error {
			if in.Bad != "" {
				return errs.New(errs.InvalidInput, "probe: --bad is refused")
			}
			return nil
		})
	}
	return o
}

// TestRunRefusesBadInputBeforeAnything (bug L-1): an input the operation's
// own check refuses is answered before any Before hook runs and before the
// app is touched (the runner here has no app: a call that reached it would
// panic); the OnError hooks still see the refusal. Break-verified: without
// the precheck in Runner.Run the Before hook runs and the nil app panics.
func TestRunRefusesBadInputBeforeAnything(t *testing.T) {
	t.Parallel()
	before, onErr := 0, 0
	r := NewRunner(nil, []Hook{{
		Name:    "probe",
		Before:  func(context.Context, *Run) error { before++; return nil },
		OnError: func(_ context.Context, _ *Run, err error) error { onErr++; return err },
	}})
	d := op.ArgsDecoder{Args: []string{"x"}, Flags: map[string][]string{"bad": {"1"}}}
	_, err := r.Run(context.Background(), precheckOp("probe.run", true), d, op.Caller{})
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "--bad is refused") {
		t.Fatalf("err = %v, want E_INVALID_INPUT --bad is refused", err)
	}
	if before != 0 || onErr != 1 {
		t.Fatalf("before hooks ran %d time(s), OnError %d: want 0 and 1", before, onErr)
	}
}

// TestPrecheckKeepsOtherRefusalsInPlace (R-10): only an operation that set
// a check is decoded early; a decode error (a missing argument) and a write
// in read-only mode are left to the normal path, so E_READ_ONLY and the
// required-field refusal keep their order.
func TestPrecheckKeepsOtherRefusalsInPlace(t *testing.T) {
	t.Parallel()
	n := 0
	bad := countingDecoder{op.ArgsDecoder{Args: []string{"x"}, Flags: map[string][]string{"bad": {"1"}}}, &n}
	if err := precheck(precheckOp("probe.none", false), bad, false); err != nil || n != 0 {
		t.Fatalf("no check: err = %v, decodes %d; want nil and 0", err, n)
	}
	if err := precheck(precheckOp("probe.ro", true), bad, true); err != nil || n != 0 {
		t.Fatalf("read-only write: err = %v, decodes %d; want nil and 0", err, n)
	}
	if err := precheck(precheckOp("probe.missing", true), countingDecoder{op.ArgsDecoder{}, &n}, false); err != nil {
		t.Fatalf("missing argument: err = %v, want nil (the normal path reports it)", err)
	}
	if err := precheck(precheckOp("probe.bad", true), bad, false); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("own check: err = %v, want E_INVALID_INPUT", err)
	}
}
