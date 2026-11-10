package value

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// TestFormsAgree: each helper gives the same answer for the in-memory form
// of a field and the form Infer decodes from its file bytes.
func TestFormsAgree(t *testing.T) {
	t.Parallel()
	id1, err := ids.ParseAny("tag_01936f3300007d4e8f5a6b7c8d9e0f01")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := ids.ParseAny("tag_01936f3300007d4e8f5a6b7c8d9e0f02")
	if err != nil {
		t.Fatal(err)
	}
	r1, _ := RefOf(id1)
	r2, _ := RefOf(id2)
	refs, _ := ListOf(KindRef, r1, r2)
	arr, _ := JSONOf([]byte(`["a:done","b"]`))
	mixed, _ := JSONOf([]byte(`["x",1,{"k":"ü"}]`))
	obj, _ := JSONOf([]byte(`{"b":1,"a":["x"]}`))
	str, _ := JSONOf([]byte(`"just text"`))
	empty, _ := ListOf(KindText)
	cases := []struct {
		name  string
		v     Value
		items []string
	}{
		{"ref list", refs, []string{id1.String(), id2.String()}},
		{"json array of text", arr, []string{"a:done", "b"}},
		{"mixed json array", mixed, []string{"x", "1", `{"k":"ü"}`}},
		{"object", obj, nil},
		{"json string", str, []string{"just text"}},
		{"empty list", empty, []string{}},
		{"single ref", r1, []string{id1.String()}},
	}
	for _, c := range cases {
		mem, ok := JSONForm(c.v)
		if !ok {
			t.Fatalf("%s: no JSON form", c.name)
		}
		dec, err := Infer(mem)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		fileForm, ok := JSONForm(dec)
		if !ok || string(fileForm) != string(mem) {
			t.Errorf("%s: JSON form %s vs decoded %s", c.name, mem, fileForm)
		}
		for form, v := range map[string]Value{"memory": c.v, "file": dec} {
			got := ItemStrings(v)
			if len(got) == 0 && len(c.items) == 0 {
				continue
			}
			if !slices.Equal(got, c.items) {
				t.Errorf("%s (%s): items %q, want %q", c.name, form, got, c.items)
			}
		}
		_, memObj := ObjectForm(c.v)
		_, fileObj := ObjectForm(dec)
		if memObj != fileObj || memObj != (c.name == "object") {
			t.Errorf("%s: ObjectForm memory %v file %v", c.name, memObj, fileObj)
		}
	}
	if _, ok := JSONForm(nil); ok {
		t.Error("nil has a JSON form")
	}
	if _, ok := JSONForm(Null{}); ok {
		t.Error("null has a JSON form")
	}
	if ItemStrings(nil) != nil || ItemStrings(Null{}) != nil {
		t.Error("nil/null items")
	}
	txt, _ := TextOf(`{"a":1}`)
	if j, ok := ObjectForm(txt); !ok || !j.IsObject() {
		t.Error("text holding an object")
	}
}
