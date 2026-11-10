package schema

// Actors-row fields the engine reads to project an agent to its owner in a
// client repo (AGENT_IDENTITY Q11/F18, P2B-OLD-2). They name columns of the
// built-in actors table (builtin.go), declared here once so the engine does
// not repeat the strings.
const (
	// FieldActorKind is actors.kind (one of ActorKinds).
	FieldActorKind = "kind"
	// FieldActorOwner is actors.owner_actor_id: the person who owns an
	// agent (empty for a person, or an agent no one owns).
	FieldActorOwner = "owner_actor_id"
)
