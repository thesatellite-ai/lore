package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Prompt: YAML-style slash command template (Continue.dev pattern, R15 #7).
// Single LLM input string with {{args.foo}} placeholders.
type Prompt struct{ ent.Schema }

func (Prompt) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixPrompt},
	}
}
func (Prompt) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("name").NotEmpty(),
		field.Text("description").NotEmpty(),
		// JSON schema describing expected args. Optional.
		field.String("args_schema").Optional().Nillable(),
		field.Text("body").NotEmpty(),
		field.Time("archived_at").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}
func (Prompt) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "name").Unique(),
	}
}
