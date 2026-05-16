package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Snapshot: large historical/reference docs. Architecture snapshots, full
// feature specs. Decay fast (30d half-life). Truncated first when over budget.
type Snapshot struct{ ent.Schema }

func (Snapshot) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixSnapshot},
		LifecycleMixin{},
	}
}
func (Snapshot) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("repo_id").Optional().Nillable(),
		field.String("title").NotEmpty(),
		field.Text("body").NotEmpty(),
		// taken_at: source age, NOT DB-write age.
		field.Time("taken_at"),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}
func (Snapshot) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "repo_id"),
		index.Fields("taken_at"),
	}
}
