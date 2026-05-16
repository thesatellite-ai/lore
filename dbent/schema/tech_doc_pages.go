package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TechDocPage belongs to a TechDoc.
type TechDocPage struct{ ent.Schema }

func (TechDocPage) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixTechDocPage}}
}
func (TechDocPage) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("tech_doc_id").NotEmpty(),
		field.String("url").NotEmpty(),
		field.String("title").Optional().Nillable(),
		field.Text("body").NotEmpty(),
		field.String("content_sha").Optional().Nillable(),
	}
}
func (TechDocPage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("tech_doc", TechDoc.Type).
			Ref("pages").
			Field("tech_doc_id").
			Unique().
			Required(),
	}
}
func (TechDocPage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tech_doc_id"),
		index.Fields("url"),
	}
}
