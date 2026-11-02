package main

import (
	"strings"
	"testing"
)

// D8: the client copy of an actor names the person and nothing that
// identifies or reaches them elsewhere.
func TestProjectActorForClient(t *testing.T) {
	in := []byte(`{"_table":"actors","_v":1,"display_name":"Alice Doe","handle":"alice","id":"act_1","kind":"human","label":"backend-1","owner_actor_id":"act_0","stable_key":"human:alice@northwind.example"}`)
	out := string(projectActorBytes(in))
	for _, gone := range []string{"stable_key", "northwind.example", "handle", "label", "backend-1", "owner_actor_id"} {
		if strings.Contains(out, gone) {
			t.Fatalf("%q must not reach a client repo: %s", gone, out)
		}
	}
	for _, kept := range []string{`"id": "act_1"`, "Alice Doe", `"kind": "human"`, `"_table": "actors"`} {
		if !strings.Contains(out, kept) {
			t.Fatalf("%q must stay: %s", kept, out)
		}
	}
}
