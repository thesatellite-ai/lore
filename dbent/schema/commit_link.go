package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// CommitLink is the polymorphic link from any entity (task, run, mission,
// decision, …) to one or more git commits. Lets users / agents anchor
// "this work shipped as <sha>" against the entity that drove the work.
//
// Polymorphic shape (entity_table + entity_id) matches comment/entity_tag.
// Same row may exist on multiple entities (e.g. one commit closing several
// tasks); we keep one row per (entity, sha) pair.
type CommitLink struct{ ent.Schema }

func (CommitLink) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixCommitLink}}
}

func (CommitLink) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("entity_table").NotEmpty().Immutable(),
		field.String("entity_id").NotEmpty().Immutable().Match(idValidatorRE),
		field.String("sha").NotEmpty().Immutable(),
		field.String("message").Optional().Nillable(),
		field.String("repo_path").Optional().Nillable(),
		field.String("author").Optional().Nillable(),
		field.String("committed_at").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}

func (CommitLink) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "entity_table", "entity_id"),
		index.Fields("project_id", "sha"),
		index.Fields("entity_table", "entity_id", "sha").Unique(),
	}
}
