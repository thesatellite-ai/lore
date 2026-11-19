package op

import (
	"context"
	"reflect"
	"testing"
)

// TestWithTopTextSplicesTheField (devco:D-623): a resource-routed read's
// --json names the folder it read in a top-level field, first; an empty
// output object gets it too, and an output field of the same name wins.
func TestWithTopTextSplicesTheField(t *testing.T) {
	type out struct {
		Items  []string `json:"items"`
		Routed string   `json:"routed,omitempty"`
	}
	type bare struct{}
	cases := []struct {
		name string
		r    Result
		want string
	}{
		{"object", Result{v: reflect.ValueOf(struct {
			Items []string `json:"items"`
		}{Items: []string{}})}.WithTopText(RoutedField, "acme/sync"), `{"routed":"acme/sync","items":[]}`},
		{"empty object", Result{v: reflect.ValueOf(bare{})}.WithTopText(RoutedField, "acme/sync"), `{"routed":"acme/sync"}`},
		{"no text", Result{v: reflect.ValueOf(bare{})}.WithTopText(RoutedField, ""), `{}`},
		{"own field wins", Result{v: reflect.ValueOf(out{Items: []string{}, Routed: "x/y"}), fields: []FieldInfo{{Name: "items"}, {Name: RoutedField}}}.WithTopText(RoutedField, "acme/sync"), `{"items":[],"routed":"x/y"}`},
	}
	for _, c := range cases {
		b, err := c.r.JSON()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(b) != c.want {
			t.Errorf("%s: JSON = %s, want %s", c.name, b, c.want)
		}
	}
}

// TestWithTopFieldsDeclaresPerOperation: the registry copy names the added
// output fields of the operations the function picks, and leaves the
// original registry without them.
func TestWithTopFieldsDeclaresPerOperation(t *testing.T) {
	type in struct{}
	type o struct{}
	run := func(context.Context, in) (o, error) { return o{}, nil }
	reg, err := NewRegistry(
		Define(Spec{Name: "thing.list", Summary: "list", Permission: PermRead, Access: AccessRead}, run),
		Define(Spec{Name: "thing.add", Summary: "add", Permission: PermWrite, Access: AccessWrite}, run),
	)
	if err != nil {
		t.Fatal(err)
	}
	c := reg.WithTopFields(func(s Spec) []FieldInfo {
		if s.Access == AccessWrite {
			return nil
		}
		return []FieldInfo{{Name: RoutedField, Type: TypeString}}
	})
	if got := c.TopFields("thing.list"); len(got) != 1 || got[0].Name != RoutedField {
		t.Errorf("thing.list top fields = %v", got)
	}
	if got := c.TopFields("thing.add"); got != nil {
		t.Errorf("thing.add top fields = %v", got)
	}
	if got := reg.TopFields("thing.list"); got != nil {
		t.Errorf("the original registry changed: %v", got)
	}
	if _, ok := c.Lookup("thing.add"); !ok {
		t.Error("the copy lost an operation")
	}
}
