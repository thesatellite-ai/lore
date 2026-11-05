package resquar

import "testing"

// TestCheck: type misfits and values outside the set quarantine; refs,
// secret_refs, unknown types, nulls and allowed values never do (3.15, 3.4).
func TestCheck(t *testing.T) {
	vals := []string{"aws", "gcp"}
	for _, c := range []struct {
		name string
		f    Field
		raw  string
		want string
	}{
		{"fits text", Field{Name: "a", Type: "text"}, `"x"`, ""},
		{"number in text", Field{Name: "a", Type: "text"}, `5`, "does not fit text"},
		{"text in number", Field{Name: "n", Type: "number"}, `"five"`, "does not fit number"},
		{"allowed", Field{Name: "p", Type: "text", Values: vals}, `"aws"`, ""},
		{"not allowed", Field{Name: "p", Type: "text", Values: vals}, `"azure"`, "not one of aws, gcp"},
		{"list items allowed", Field{Name: "p", Type: "list", Values: vals}, `["aws","gcp"]`, ""},
		{"list item not allowed", Field{Name: "p", Type: "list", Values: vals}, `["aws","x"]`, "not one of aws, gcp"},
		{"null with values", Field{Name: "p", Type: "text", Values: vals}, `null`, ""},
		{"dangling ref", Field{Name: "r", Type: "ref"}, `"res_nope"`, ""},
		{"secret_ref", Field{Name: "s", Type: "secret_ref"}, `5`, ""},
		{"unknown type", Field{Name: "u", Type: "hologram"}, `5`, ""},
	} {
		got, bad := Check(c.f, []byte(c.raw))
		if (c.want != "") != bad || got.Rule != c.want {
			t.Errorf("%s: got %v %v, want %q", c.name, got, bad, c.want)
		}
	}
}
