package stores

import "testing"

// D8 / Q11 matrix: person / agent with owner / agent without owner ×
// agent_details internal / client.
func TestClientActorProjection(t *testing.T) {
	human := map[string]any{"_table": "actors", "id": "act_h", "display_name": "Alice Doe", "kind": "human", "stable_key": "human:alice@northwind.example", "handle": "alice"}
	agent := map[string]any{"_table": "actors", "id": "act_a", "display_name": "backend-1", "kind": "agent", "label": "backend-1", "owner_actor_id": "act_h", "stable_key": "agent:backend-1"}
	cases := []struct {
		name             string
		actor, owner     map[string]any
		client           bool
		wantID, wantName string
		wantKind         string
	}{
		{"person", human, nil, false, "act_h", "Alice Doe", "human"},
		{"agent → owning human", agent, human, false, "act_h", "Alice Doe", "human"},
		{"agent without owner → org name", agent, nil, false, "act_a", "Northwind", "human"},
		{"agent_details client → agent itself", agent, human, true, "act_a", "backend-1", "agent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClientActorProjection(c.actor, c.owner, "Northwind", c.client)
			if got["id"] != c.wantID || got["display_name"] != c.wantName || got["kind"] != c.wantKind {
				t.Fatalf("got %v", got)
			}
			for _, k := range []string{"stable_key", "handle", "label", "owner_actor_id"} {
				if _, ok := got[k]; ok {
					t.Fatalf("%s must never reach a client repo: %v", k, got)
				}
			}
		})
	}
}
