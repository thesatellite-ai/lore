package op

import (
	"context"
	"slices"
	"testing"
)

// color is a closed set; shade repeats a value; blank lists nothing.
type (
	color string
	shade string
	blank string
)

func (color) EnumValues() []string { return []string{"red", "green"} }
func (shade) EnumValues() []string { return []string{"dark", "dark"} }
func (blank) EnumValues() []string { return nil }

type enumIn struct {
	One   color   `flag:"one" json:"one,omitempty"`
	Many  []color `flag:"many" json:"many,omitempty"`
	Plain string  `flag:"plain" json:"plain,omitempty"`
}

type enumOut struct {
	One  color       `json:"one"`
	Null Null[color] `json:"null"`
	Rows []struct {
		C color `json:"c"`
	} `json:"rows"`
}

// TestEnumFromEnumerated: a field of an op.Enumerated type (alone, in a
// slice, as op.Null, nested in an output list) carries the type's value
// set; a plain string carries none.
func TestEnumFromEnumerated(t *testing.T) {
	t.Parallel()
	e := Define(Spec{Name: "enum.show"}, func(context.Context, enumIn) (enumOut, error) { return enumOut{}, nil })
	want := []string{"red", "green"}
	in := e.InputFields()
	out := e.OutputFields()
	cases := []struct {
		name string
		f    FieldInfo
		want []string
	}{
		{"input string", in[0], want},
		{"input list", in[1], want},
		{"plain string", in[2], nil},
		{"output string", out[0], want},
		{"output null", out[1], want},
		{"nested in a list", out[2].Fields[0], want},
	}
	for _, c := range cases {
		if !slices.Equal(c.f.Enum, c.want) {
			t.Errorf("%s (%s): enum %v want %v", c.name, c.f.Name, c.f.Enum, c.want)
		}
	}
}

// TestEnumRefusesBadSets: a repeated or empty value set is a programmer
// error caught at Define time.
func TestEnumRefusesBadSets(t *testing.T) {
	t.Parallel()
	type dup struct {
		S shade `json:"s"`
	}
	type empty struct {
		B blank `json:"b"`
	}
	for name, f := range map[string]func(){
		"repeated": func() {
			Define(Spec{Name: "a.b"}, func(context.Context, dup) (struct{}, error) { return struct{}{}, nil })
		},
		"empty": func() {
			Define(Spec{Name: "a.b"}, func(context.Context, empty) (struct{}, error) { return struct{}{}, nil })
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
}
