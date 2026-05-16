package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Playbook: reusable workflow (Voyager-style executable skill, R15 #14).
// Composable via JSON array of playbook IDs in `composes`.
type Playbook struct{ ent.Schema }

func (Playbook) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixPlaybook},
		LifecycleMixin{},
	}
}
func (Playbook) Fields() []ent.Field {
	return []ent.Field{
		// project_id NULL = global playbook.
		field.String("project_id").Optional().Nillable(),
		field.String("name").NotEmpty(),
		field.Text("description").NotEmpty(),
		field.Text("body").NotEmpty(),
		// JSON array of playbook IDs this composes. NULL = atomic.
		field.String("composes").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}
func (Playbook) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id"),
	}
}
