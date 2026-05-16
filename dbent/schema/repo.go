package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Repo is a code repository within a project (one project, many repos).
//
// Identity: mount_name is the per-project handle. UNIQUE within project
// (so --repo=web1 always resolves to one row). NOT unique globally.
//
// Same logical repo (origin_url) can be mounted in multiple projects under
// different mount names — see PLAN.md Round 20.
//
// NO `path` column: per-machine paths conflict in multi-user. cwd-based
// resolution is replaced by explicit `--repo=` flag at command boundary.
type Repo struct {
	ent.Schema
}

func (Repo) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixRepo},
	}
}

func (Repo) Fields() []ent.Field {
	return []ent.Field{
		// project_id: FK to owning project (declared via edge below).
		field.String("project_id").
			NotEmpty().
			Immutable(),

		// mount_name: structural handle. User types this:
		//   aicoder memory add ... --repo=web1
		// Soft-renamable via `aicoder repo rename`; old name preserved
		// in mount_aliases for 30 days then errors.
		field.String("mount_name").
			NotEmpty(),

		// display_name: free-form, optional. Used in render output:
		//   [repo:web1] (Web Frontend)
		field.String("display_name").
			Optional().
			Nillable(),

		// origin_url: per-repo git remote, optional. Cross-project queries
		// can find "all places that use shared-frontend" via origin_url match.
		field.String("origin_url").
			Optional().
			Nillable(),

		field.Time("archived_at").
			Optional().
			Nillable(),
	}
}

func (Repo) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).
			Ref("repos").
			Field("project_id").
			Unique().
			Required().
			Immutable(),
		edge.To("memories", Memory.Type),
	}
}

func (Repo) Indexes() []ent.Index {
	return []ent.Index{
		// mount_name UNIQUE within project per Round 20.
		index.Fields("project_id", "mount_name").Unique(),
	}
}
