package stores

// Actor kinds the projection distinguishes (dbent/schema/actor.go).
const (
	actorKindHuman = "human"
	actorKindAgent = "agent"
)

// clientActorKeys is the only content a client repo may hold for an actor
// (F18 client copy, F16 exception): {id, display name, kind}, plus the
// engine's file bookkeeping.
var clientActorKeys = []string{"_table", "_v", "id", "display_name", "kind", "created_at", "updated_at"}

// ClientActorProjection is the actor file a CLIENT repo may hold for an
// actor a client row references (F18, D8, TEAM-018, coordinator Q11):
//
//   - a person → {id, display_name, kind}: never stable_key (their email in
//     the org), owner_actor_id, handle or label;
//   - an agent → its OWNING HUMAN's projection (or, with no owner, the org
//     display name as a kind-human stand-in keeping the agent's id), so a
//     client never sees an agent label or agent id — unless the org sets
//     agent_details: client (agentDetailsClient), then the agent's own
//     {id, display_name, kind}.
//
// owner is the owning human's full actor document (nil when unknown).
// Callers that write a client row naming an agent must name the id this
// returns (it is the owner's id for agents), so the row and the actor file
// agree.
func ClientActorProjection(actor, owner map[string]any, orgDisplayName string, agentDetailsClient bool) map[string]any {
	kind, _ := actor["kind"].(string)
	if kind == actorKindAgent && !agentDetailsClient {
		if owner != nil {
			return pick(owner)
		}
		out := pick(actor)
		out["display_name"] = orgDisplayName
		out["kind"] = actorKindHuman
		return out
	}
	return pick(actor)
}

func pick(doc map[string]any) map[string]any {
	out := make(map[string]any, len(clientActorKeys))
	for _, k := range clientActorKeys {
		if v, ok := doc[k]; ok {
			out[k] = v
		}
	}
	return out
}
