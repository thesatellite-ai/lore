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
//   - agent   AI agent (Claude Code, Cursor, Codex CLI)
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
		//   human:amank@example.com         from git config user.email
		//   agent:claude-code               from LORE_ACTOR
		//   auto:a3f9c1k2                   sha256(machine-id)[:12]
		//   auto:ephemeral-7f3a2c           last-resort session salt
		//   anon:9e8d7c6b                   anon mode
		field.String("stable_key").
			NotEmpty().
			Unique().
			Immutable(),

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
	}
}
