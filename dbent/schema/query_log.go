package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// QueryLog: log of search/retrieve/assemble calls. Debugging spine (R22 #13).
// 30-day retention by default; pruned at session-start hook.
type QueryLog struct{ ent.Schema }

func (QueryLog) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixQueryLog},
	}
}
func (QueryLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").Optional().Nillable(),
		field.String("actor_id").Optional().Nillable(),
		// 'search' | 'retrieve' | 'assemble'
		field.String("command").NotEmpty(),
		field.Text("query_text").Optional().Nillable(),
		// JSON of scope flags: {"repo":"web1","no_inherit":true}
		field.String("scope_flags").Optional().Nillable(),
		field.Int("result_count").Optional().Nillable(),
		field.Int("latency_ms").Optional().Nillable(),
	}
}
func (QueryLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id"),
	}
}
