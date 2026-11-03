package schema

import (
	"saas/pkg/aicoder/ids"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Actor identifies who/what performed a write. Replaces scattered
// "actor" / "actor_kind" columns across knowledge tables (R37 Block 2).
//
// 6 kinds:
//   - human   real person (resolved via git config / env / prompt)
//   - agent   coding agent (any agent CLI or editor integration)
//   - hook    git/editor hook script invoking aicoder
//   - plugin  registered mini plugin (Linear, Slack, etc.)
//   - cron    scheduled task
//   - system  mini itself (migrations, doctor self-checks, repair)
//
// Resolution chain (R32) populates stable_key first; lookup-or-insert is
// performed at first write. Same email across machines maps to the same
// row via stable_key UNIQUE constraint.
//
// Catches: R32 (8-step chain), R37 Block 2 (actors table).
type Actor struct {
	ent.Schema
}

func (Actor) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixActor},
	}
}

func (Actor) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("kind").
			Values(actorKindValues...),

		// display_name: shown in CLI output / render citations / errors.
		// Mutable; not used for resolution (id is the stable handle).
		// NOT unique (collisions allowed per R20).
		field.String("display_name").
			NotEmpty(),

		// stable_key: resolved identity from R32 chain. Unique because
		// two rows with same key would corrupt cross-machine merging.
		// Format examples (R32):
		//   human:alice@example.com         from git config user.email
		//   agent:my-agent                  from LORE_ACTOR
		//   auto:a3f9c1k2                   sha256(machine-id)[:12]
		//   auto:ephemeral-7f3a2c           last-resort session salt
		//   anon:9e8d7c6b                   anon mode
		field.String("stable_key").
			NotEmpty().
			Unique().
			Immutable(),

		// ---- roster (LORE_TEAM_SPEC F18, §10 C15) ----------------------
		// All optional/defaulted so actors written before the roster
		// existed stay valid. The roster lives in the org folder of the
		// org's store; a client repo only ever receives the projection
		// {id, display_name, kind}, never owner_actor_id or stable_key.

		// owner_actor_id: for kind=agent, the human actor that runs it.
		field.String("owner_actor_id").Optional().Nillable(),
		// label: for kind=agent, the session label (`frontend-2`); one
		// agent actor per label.
		field.String("label").Optional().Nillable(),
		// handle: display alias used as @handle. Unique per roster, which
		// the writer checks against actors AND teams of the roster; not a
		// DB constraint because one cache can hold several projects' rows.
		field.String("handle").Optional().Nillable(),
		// active: false once deactivated; actors are never deleted, so old
		// rows that cite them still resolve.
		field.Bool("active").Default(true),
		// left_at: when the actor was deactivated.
		field.Time("left_at").Optional().Nillable(),

		// last_seen_at: updated lazily; surfaced by `aicoder identity show`.
		field.Time("last_seen_at").
			Default(time.Now),
	}
}

func (Actor) Edges() []ent.Edge {
	return []ent.Edge{
		// Future: edges to memories/decisions/etc. via created_by_actor_id
		// will be added when those schemas land. Defining the inverse here
		// keeps imports tidy.
		edge.To("created_memories", Memory.Type),
		edge.To("validated_memories", Memory.Type),
	}
}

func (Actor) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("kind"),
		index.Fields("handle"),
	}
}
