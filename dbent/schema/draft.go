package schema

import (
	"saas/pkg/aicoder/ids"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Draft: in-progress editor session (R29 #71). Recoverable on next start.
// Cleanup: drafts older than 24h pruned at maintenance.
type Draft struct{ ent.Schema }

func (Draft) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixDraft},
	}
}
func (Draft) Fields() []ent.Field {
	return []ent.Field{
		field.String("target_table").NotEmpty(),
		// NULL = drafting NEW entity.
		field.String("target_id").Optional().Nillable(),
		field.Text("body").NotEmpty(),
		field.String("actor_id").Optional().Nillable(),
		field.Time("started_at").Default(time.Now),
		// pid of process holding the edit. Validate alive before refusing concurrent edit.
		field.Int("pid").Optional().Nillable(),
	}
}
