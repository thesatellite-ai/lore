package actorproject

// client.go — FLAG6 (owner A): the minimal actor row a client repo gets
// for an actor a client-visible row names (assignee, done_by, reviewer,
// ...). One definition, below wire: v0.1.17 kept it as
// saas/pkg/aicoder/stores.ClientActorProjection, which wire could not
// import (later 385), so wire rebuilt it inline; wire now calls this.
//
// The projection is {id, display_name, kind} and nothing else: never
// stable_key, handle or email, because every clone of the client repo
// reads the file (F18). The id is the row id, so only the two fields are
// computed here.

// Field names of the client projection (the actors table's columns).
const (
	FieldKind        = "kind"
	FieldDisplayName = "display_name"
)

// KindAgent is the actors.kind of an agent (schema.ActorKindAgent; this
// package imports model only, so the closed value is named here and the
// wire test pins it against the schema constant).
const KindAgent = "agent"

// ClientActor is the projection of one actor for a client repo.
type ClientActor struct {
	Kind        string
	DisplayName string
}

// ClientProjection decides the client copy of an actor whose own row
// (in the first attached store that has it) has kind and display. ok is
// false when nothing is written:
//   - display is empty: display_name is required, an actor without one
//     cannot be projected;
//   - an agent while the org's agent_details is not "client": its
//     projection would be its owner under another id, and the row names
//     the agent (the engine maps that id to the owner itself, P2B-OLD-2).
func ClientProjection(kind, display string, agentDetailsClient bool) (ClientActor, bool) {
	if display == "" {
		return ClientActor{}, false
	}
	if kind == KindAgent && !agentDetailsClient {
		return ClientActor{}, false
	}
	return ClientActor{Kind: kind, DisplayName: display}, true
}

// Fields is the projection as column values, empty values left out.
func (c ClientActor) Fields() map[string]string {
	out := map[string]string{}
	if c.Kind != "" {
		out[FieldKind] = c.Kind
	}
	if c.DisplayName != "" {
		out[FieldDisplayName] = c.DisplayName
	}
	return out
}

// Patch is the columns of c that differ from the client repo's current
// row (cur reads one column as text): empty when the row already matches,
// so a matching row is left untouched.
func (c ClientActor) Patch(cur func(field string) string) map[string]string {
	out := map[string]string{}
	for k, v := range c.Fields() {
		if cur(k) != v {
			out[k] = v
		}
	}
	return out
}
