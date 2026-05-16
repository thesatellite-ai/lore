package schema

import (
	"saas/pkg/aicoder/ids"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

// DBConfig stores DB-instance-wide singleton settings (R36 Layer 1).
//
// Go type is named DBConfig to avoid clashing with ent's predeclared
// `config` identifier. The SQL table name stays "config" via the
// entsql.Annotation below — matches PLAN.md Round 38.
//
// One row per key. Used for:
//   - schema_version       — replay log integrity check on boot
//   - db_uuid              — unique per DB instance; appears in exports/audit
//   - db_created_at        — informational
//   - last_vacuum_at       — surfaced by doctor
//   - last_checkpoint_at   — WAL checkpoint tracking
//
// Does NOT contain:
//   - per-project settings (those go in project_config — R36 L2)
//   - per-machine settings (those go in ~/.lore/config.toml — L3)
//   - runtime state (resolved per-invocation — L4)
type DBConfig struct {
	ent.Schema
}

func (DBConfig) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "config"},
	}
}

func (DBConfig) Mixin() []ent.Mixin {
	return []ent.Mixin{
		// Mixin gives a string UUIDv7 id consistent with the rest of the
		// schema. The natural unique key is `key` (column-level UNIQUE).
		AicoderBaseMixin{Prefix: ids.PrefixDBConfig},
	}
}

func (DBConfig) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").NotEmpty().Immutable().Unique(),
		field.Text("value").Optional().Nillable(),
		field.Time("setting_updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
