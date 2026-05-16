package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// SchemaMigration: replay log of applied migrations (R21 #49).
// Used for boot-time integrity check + power-loss-mid-migration recovery.
type SchemaMigration struct{ ent.Schema }

func (SchemaMigration) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixSchemaMigration},
	}
}
func (SchemaMigration) Fields() []ent.Field {
	return []ent.Field{
		field.Int("version").Unique(),
		// sha256 of the migration source file. Verified on boot.
		field.String("migration_sha256").NotEmpty(),
		// sha256 of the full schema after this migration applied.
		field.String("schema_sha256").NotEmpty(),
		// 'applied' = clean COMMIT. 'in_progress' = mid-flight crash.
		field.Enum("status").Values(schemaMigrationStatusValues...),
	}
}
