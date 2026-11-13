package op

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// listIn has one nonempty flag (a list the op would otherwise default) and
// one plain flag, so the test sees the tag change only its own field.
type listIn struct {
	Title    string `arg:"0" json:"title,omitempty"`
	Tasklist string `flag:"tasklist" nonempty:"a tasklist id" json:"tasklist,omitempty"`
	Note     string `flag:"note" json:"note,omitempty"`
}

func listOp() *Op[listIn, listIn] {
	return Define(Spec{Name: "task.add", Summary: "s", Permission: PermWrite, Access: AccessWrite},
		func(_ context.Context, in listIn) (listIn, error) { return in, nil })
}

// TestNonEmptyFlag (U-3): `--tasklist=` given with nothing is refused
// with E_INVALID_INPUT naming the flag, instead of decoding as "not
// given" (which made task add attach the task to the default list). An
// absent flag, a real value, a plain flag's empty value and a JSON ""
// all decode as before.
func TestNonEmptyFlag(t *testing.T) {
	t.Parallel()
	o := listOp()
	for _, v := range []string{"", "  "} {
		_, err := o.Decode(ArgsDecoder{Args: []string{"x"}, Flags: map[string][]string{"tasklist": {v}}})
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "--tasklist is empty; give a tasklist id") {
			t.Errorf("--tasklist=%q: err = %v", v, err)
		}
	}
	for name, d := range map[string]Decoder{
		"absent":      ArgsDecoder{Args: []string{"x"}},
		"value":       ArgsDecoder{Args: []string{"x"}, Flags: map[string][]string{"tasklist": {"tlt_x"}}},
		"plain empty": ArgsDecoder{Args: []string{"x"}, Flags: map[string][]string{"note": {""}}},
		"json empty":  JSONDecoder{Body: []byte(`{"title":"x","tasklist":""}`)},
	} {
		if _, err := o.Decode(d); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	var got FieldInfo
	for _, f := range o.InputFields() {
		if f.Name == "tasklist" {
			got = f
		}
	}
	if got.NonEmpty != "a tasklist id" {
		t.Fatalf("FieldInfo.NonEmpty = %q", got.NonEmpty)
	}
}

// TestNonEmptyTagMisuse: the tag goes on a string flag and names what to
// give; anything else is a Define-time panic, like every other bad tag.
func TestNonEmptyTagMisuse(t *testing.T) {
	t.Parallel()
	type onArg struct { // a nonempty bool is no string
		A bool `nonempty:"x" json:"a" flag:"a"`
	}
	type blank struct {
		A string `flag:"a" nonempty:"" json:"a"`
	}
	type onBool struct {
		A bool `flag:"a" nonempty:"x" json:"a"`
	}
	check := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		f()
	}
	check("on a bool", func() {
		Define(Spec{Name: "a.b"}, func(context.Context, onArg) (struct{}, error) { return struct{}{}, nil })
	})
	check("blank", func() {
		Define(Spec{Name: "a.b"}, func(context.Context, blank) (struct{}, error) { return struct{}{}, nil })
	})
	check("on a bool", func() {
		Define(Spec{Name: "a.b"}, func(context.Context, onBool) (struct{}, error) { return struct{}{}, nil })
	})
}
