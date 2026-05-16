package schema

import (
	"regexp"
	"saas/pkg/aicoder/ids"
	"time"

	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

// AicoderBaseMixin is the canonical mixin for all lore entities.
//
// Differs from BaseMixin (used by the existing User entity in this monorepo)
// by using UUIDv7 instead of nanoid, with prefix conventions per
// PLAN.md Round 31 + 34.
//
// Each entity that uses this mixin sets a Prefix matching its kind from the
// ids registry (e.g., ids.PrefixMemory, ids.PrefixProject).
//
// Catches: R31 (UUIDv7), R34.1 (keep created_at), R34.4 (id+created_at sort),
// R34.5 (DB CHECK + app validation).
type AicoderBaseMixin struct {
	mixin.Schema
	Prefix string // 3-letter prefix from ids registry; required
}

// idValidatorRE enforces the <prefix>_<32-hex> format.
var idValidatorRE = regexp.MustCompile(`^[a-z]{3}_[0-9a-f]{32}$`)

// Fields of AicoderBaseMixin.
func (m AicoderBaseMixin) Fields() []ent.Field {
	prefix := m.Prefix
	if prefix == "" {
		// Default to memory prefix; entity authors should always set Prefix
		// explicitly. Caught by tests that exercise New/Validate.
		prefix = ids.PrefixMemory
	}
	return []ent.Field{
		field.String("id").
			DefaultFunc(func() string {
				return ids.MustNew(prefix)
			}).
			Match(idValidatorRE).
			Immutable().
			Unique(),

		// created_at kept distinct from UUIDv7 ID-gen time per Round 34.1:
		//   - id-time = when the row was assigned an ID
		//   - created_at = when the row was inserted (may differ for
		//     bulk-imports representing year-old facts)
		// Used as the ORDER BY tiebreaker per Round 34.4.
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Annotations(
				entgql.OrderField("CREATED_AT"),
				SkipMutations,
			),

		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			Annotations(SkipMutations),
	}
}

// LifecycleMixin provides the uniform lifecycle columns required on every
// knowledge table per PLAN.md Round 37 Block 3.
//
// Columns:
//   - trust_score      — 0.0..1.0, source-dependent default
//   - confidence       — nullable, LLM-graded (v0.2)
//   - last_accessed_at — lazy update via decay buffer (R27 #1)
//   - last_validated_at — review tracking
//   - archived_at      — soft-delete sentinel
//   - superseded_by_id — replacement chain (FK to same table; declared per-entity)
//   - source_kind      — manual | learn-from | agent-proposal | plugin | imported | migrated
//   - source_ref       — free-form provenance pointer
type LifecycleMixin struct {
	mixin.Schema
}

// Fields of LifecycleMixin.
func (LifecycleMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Float("trust_score").
			Default(0.5).
			Min(0).Max(1),

		field.Float("confidence").
			Optional().
			Nillable().
			Min(0).Max(1),

		field.Time("last_accessed_at").
			Optional().
			Nillable(),

		field.Time("last_validated_at").
			Optional().
			Nillable(),

		field.Time("archived_at").
			Optional().
			Nillable(),

		field.String("source_kind").
			NotEmpty(),

		field.String("source_ref").
			Optional().
			Nillable(),
	}
}
