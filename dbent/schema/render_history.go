package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RenderHistory: per-render snapshot (R22 #14). Per-project ring buffer 50;
// global hard cap 1000. Older auto-pruned at session-start.
type RenderHistory struct{ ent.Schema }

func (RenderHistory) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixRenderHistory},
	}
}
func (RenderHistory) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		field.String("repo_id").Optional().Nillable(),
		// 'CLAUDE.md' (v0.1). Multi-target adds AGENTS.md / .cursor/rules at v0.2.
		field.String("target_path").NotEmpty(),
		// Full text emitted. Bounded by render budget (default 200KB).
		field.Text("rendered_text").NotEmpty(),
		// sha256 — used by atomic-write to skip rename-if-unchanged.
		field.String("rendered_sha256").NotEmpty(),
		field.Int("total_bytes"),
		field.Int("total_tokens").Optional().Nillable(),
		// JSON breakdown: {included: {...}, excluded: {...}}
		field.Text("scope_summary").NotEmpty(),
		field.String("rendered_by_actor_id").Optional().Nillable(),
	}
}
func (RenderHistory) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id"),
	}
}
