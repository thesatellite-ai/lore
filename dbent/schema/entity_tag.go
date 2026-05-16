package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// EntityTag: M2M bridge between any entity and tags. Polymorphic.
type EntityTag struct{ ent.Schema }

func (EntityTag) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixEntityTag}}
}
func (EntityTag) Fields() []ent.Field {
	return []ent.Field{
		field.String("entity_table").NotEmpty(),
		field.String("entity_id").NotEmpty().Match(idValidatorRE),
		field.String("tag_id").NotEmpty().Match(idValidatorRE),
	}
}
func (EntityTag) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("entity_table", "entity_id", "tag_id").Unique(),
		index.Fields("tag_id"),
	}
}
