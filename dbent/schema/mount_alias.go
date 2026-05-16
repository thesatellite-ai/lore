package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MountAlias: soft-rename history for repo mount_name (R18 #28).
type MountAlias struct{ ent.Schema }

func (MountAlias) Mixin() []ent.Mixin {
	return []ent.Mixin{AicoderBaseMixin{Prefix: ids.PrefixMountAlias}}
}
func (MountAlias) Fields() []ent.Field {
	return []ent.Field{
		field.String("repo_id").NotEmpty(),
		field.String("old_name").NotEmpty(),
		field.Time("renamed_at"),
		field.Time("expires_at"),
	}
}
func (MountAlias) Indexes() []ent.Index {
	return []ent.Index{index.Fields("repo_id", "old_name").Unique()}
}
