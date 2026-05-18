package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ArchitectureNote — see PLAN.md for table semantics.
type ArchitectureNote struct{ ent.Schema }

func (ArchitectureNote) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixArchitectureNote}}
}
func (ArchitectureNote) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		// repo_id: NULL = project-master scope. Set = repo-specific.
		// Same scoping axis as memory/rule/decision/hotfix/pattern.
		// Field-only (no ent edge): scope filters use the field
		// predicate, not graph traversal, so Repo's schema stays untouched.
		field.String("repo_id").
			Optional().
			Nillable(),
		field.String("title").NotEmpty(),
		field.Text("body").NotEmpty(),
		field.String("created_by_actor_id").NotEmpty().Optional().Nillable(),
	}
}
func (ArchitectureNote) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "repo_id"),
	}
}
