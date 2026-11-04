package op

import (
	"context"
	"encoding/json"
	"testing"
)

type nullBrief struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type nullOut struct {
	Plan  Null[nullBrief] `json:"plan" desc:"the plan, null when none"`
	Count Null[int]       `json:"count"`
	Name  Null[string]    `json:"name"`
}

// TestNullJSON: unset prints null, set prints the value (zero values
// included), SomeIf treats the zero value as unset, and every case reads
// back to the same Null.
func TestNullJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   nullOut
		want string
	}{
		{"all unset", nullOut{}, `{"plan":null,"count":null,"name":null}`},
		{"set zero values", nullOut{Count: Some(0), Name: Some("")}, `{"plan":null,"count":0,"name":""}`},
		{"set", nullOut{Plan: Some(nullBrief{ID: "pln_1", Title: "ü ✓"}), Count: Some(-3), Name: Some("x")}, `{"plan":{"id":"pln_1","title":"ü ✓"},"count":-3,"name":"x"}`},
		{"SomeIf zero", nullOut{Plan: SomeIf(nullBrief{}), Count: SomeIf(0), Name: SomeIf("")}, `{"plan":null,"count":null,"name":null}`},
		{"SomeIf value", nullOut{Count: SomeIf(7), Name: SomeIf("n")}, `{"plan":null,"count":7,"name":"n"}`},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.in)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != c.want {
			t.Errorf("%s: %s, want %s", c.name, b, c.want)
		}
		var back nullOut
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if back != c.in {
			t.Errorf("%s: round trip %+v, want %+v", c.name, back, c.in)
		}
	}
	var n Null[int]
	if err := json.Unmarshal([]byte(" null \n"), &n); err != nil {
		t.Fatal(err)
	}
	if _, set := n.Get(); set {
		t.Error("padded null read as set")
	}
	if err := json.Unmarshal([]byte(`"x"`), &n); err == nil {
		t.Error("wrong type accepted")
	}
	n = Some(5)
	if err := json.Unmarshal([]byte("null"), &n); err != nil {
		t.Fatal(err)
	}
	if v, set := n.Get(); set || v != 0 {
		t.Errorf("null over a set value left %d %v", v, set)
	}
}

// TestNullFieldInfo: an output Null is described by its element type with
// Nullable set (and stays required: the key is always printed); a Null in
// an input struct is a programmer error that Define refuses.
func TestNullFieldInfo(t *testing.T) {
	t.Parallel()
	o := Define(Spec{Name: "plan.show"}, func(context.Context, struct{}) (nullOut, error) { return nullOut{}, nil })
	byName := map[string]FieldInfo{}
	for _, f := range o.OutputFields() {
		byName[f.Name] = f
	}
	for _, name := range []string{"plan", "count", "name"} {
		f, ok := byName[name]
		if !ok || !f.Nullable || !f.Required {
			t.Errorf("%s: %+v", name, f)
		}
	}
	if byName["count"].Type != TypeInt || byName["name"].Type != TypeString || byName["plan"].Type != TypeObject || len(byName["plan"].Fields) != 2 {
		t.Errorf("types %+v", byName)
	}
	type nullIn struct {
		Count Null[int] `flag:"count" json:"count"`
	}
	defer func() {
		if recover() == nil {
			t.Error("Define accepted an input Null")
		}
	}()
	Define(Spec{Name: "plan.add"}, func(context.Context, nullIn) (struct{}, error) { return struct{}{}, nil })
}
