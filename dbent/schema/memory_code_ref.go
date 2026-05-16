package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MemoryCodeRef: typed M2M memories ↔ code_files (R37 Block 4).
type MemoryCodeRef struct{ ent.Schema }

func (MemoryCodeRef) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixMemoryCodeRef}}
}
func (MemoryCodeRef) Fields() []ent.Field {
	return []ent.Field{
		field.String("memory_id").NotEmpty(),
		field.String("code_file_id").NotEmpty(),
		field.Enum("relation").Values(memoryCodeRefRelationValues...),
	}
}
func (MemoryCodeRef) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("memory_id", "code_file_id", "relation").Unique(),
		index.Fields("code_file_id"),
	}
}
