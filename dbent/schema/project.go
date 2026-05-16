package schema

import (
	"saas/pkg/aicoder/ids"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/khanakia/entx/enttui"
)

// Project is the top-level engagement / product identity.
//
// Mode A: ONE row per DB (the project this DB belongs to).
// Mode B: MANY rows per DB (one per project sharing the DB).
//
// Naming: name is DISPLAY ONLY, NOT unique (R20). Same name allowed across
// multiple rows. CLI lookup by name refuses on collision; user must use opaque ID.
// Auto-suggested from git remote on register.
//
// Lifecycle: archived projects skip vacuum/embed-refresh; resurrected projects
// trigger re-validation pass for memories > 90 days old (R22 #16-17).
type Project struct {
	ent.Schema
}

func (Project) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixProject},
	}
}

func (Project) Annotations() []schema.Annotation {
	return []schema.Annotation{
		// Multi-edge master-detail: Project → repos + memories. In the
		// split (`m`) the detail pane is tabbed [ repos | memories ].
		enttui.DetailEdge{Edges: []string{"repos", "memories"}},
	}
}

func (Project) Fields() []ent.Field {
	return []ent.Field{
		// name: display only. Mutable. NOT unique. Auto-suggested from
		// git remote `org/repo` (repo part) on register.
		field.String("name").
			NotEmpty(),

		// origin_url: master git remote URL, optional. Used by doctor to
		// detect drift if remote changes. Not used as identity.
		field.String("origin_url").
			Optional().
			Nillable(),

		// archived_at: soft-archive (R22 #16). NULL = active. Archived projects
		// excluded from default search/render; frozen (no decay applied).
		field.Time("archived_at").
			Optional().
			Nillable(),

		// last_active_at: bumped on every write to this project's rows.
		// Surfaced in `aicoder project list` for stale-project visibility.
		field.Time("last_active_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

func (Project) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("repos", Repo.Type),
		edge.To("memories", Memory.Type),
	}
}

func (Project) Indexes() []ent.Index {
	return []ent.Index{
		// Fast lookup by name (collisions allowed; CLI handles disambiguation).
		index.Fields("name"),
	}
}
