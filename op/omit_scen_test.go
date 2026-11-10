package op

// omit_scen_test.go — which output members the OpenAPI document may call
// required: a json tag with omitempty OR omitzero lets encoding/json leave
// the member out, so it is never required; a plain tag is. Before the fix
// omitzero members (task rollup/relations, whoami setup, …) were declared
// required while the real JSON omitted them, so a client validating
// `lore api` answers against openapi.json rejected them (found by
// tests/userscen TestRestOpenAPI_ResponsesMatchDeclaredSchemas).

import (
	"context"
	"encoding/json"
	"testing"
)

type omitInner struct {
	N int `json:"n"`
}

type omitOut struct {
	Plain     string    `json:"plain"`
	Empty     string    `json:"empty,omitempty"`
	Zero      omitInner `json:"zero,omitzero"`
	Both      string    `json:"both,omitempty,omitzero"`
	ZeroFirst string    `json:"zero_first,omitzero,omitempty"`
	Nested    omitInner `json:"nested"`
	Flag      bool      `json:"flag"`
	// A member NAMED like an option is still required: only the options
	// after the first comma count.
	Named string `json:"omitzero_name"`
}

type omitIn struct {
	Text string `json:"text" flag:"text"`
	Opt  string `json:"opt,omitzero" flag:"opt"`
}

// TestOmitZeroIsNotRequired pins Required per tag form and checks it
// against what encoding/json really prints for a zero value.
func TestOmitZeroIsNotRequired(t *testing.T) {
	t.Parallel()
	e := Define(Spec{Name: "omit.show", Summary: "omit", Permission: PermRead, Access: AccessRead},
		func(context.Context, omitIn) (omitOut, error) { return omitOut{}, nil })
	want := map[string]bool{"plain": true, "empty": false, "zero": false, "both": false, "zero_first": false, "nested": true, "flag": false, "omitzero_name": true}
	b, err := json.Marshal(omitOut{})
	if err != nil {
		t.Fatal(err)
	}
	printed := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &printed); err != nil {
		t.Fatal(err)
	}
	for _, f := range e.OutputFields() {
		w, ok := want[f.Name]
		if !ok {
			t.Errorf("unexpected field %s", f.Name)
			continue
		}
		if f.Required != w {
			t.Errorf("%s: Required %v, want %v", f.Name, f.Required, w)
		}
		// The invariant behind the rule: a required non-bool member is
		// always printed, even at its zero value.
		if _, there := printed[f.Name]; f.Required && !there {
			t.Errorf("%s is required but a zero value omits it: %s", f.Name, b)
		}
	}
	in := e.InputFields()
	if !in[0].Required || in[1].Required {
		t.Errorf("inputs: text required %v, opt (omitzero) required %v", in[0].Required, in[1].Required)
	}
	// Decoding an input without the omitzero member is accepted.
	if _, err := e.Decode(JSONDecoder{Body: []byte(`{"text":"x"}`)}); err != nil {
		t.Errorf("omitzero input refused when absent: %v", err)
	}
}
