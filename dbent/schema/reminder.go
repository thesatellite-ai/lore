package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Reminder: time-based reminders (R22 #28). Surfaced via daily digest hook.
// Recurring (weekly review) via recurrence pattern.
type Reminder struct{ ent.Schema }

func (Reminder) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixReminder},
	}
}
func (Reminder) Fields() []ent.Field {
	return []ent.Field{
		field.String("project_id").NotEmpty().Immutable(),
		// Optional target entity. NULL = standalone reminder.
		field.String("target_table").Optional().Nillable(),
		field.String("target_id").Optional().Nillable(),
		field.Time("due_at"),
		field.Text("message").NotEmpty(),
		// Recurrence pattern. NULL = one-shot.
		// Typed enum so generated entReminder.Recurrence7d etc. are
		// the only valid values (no string ambiguity).
		field.Enum("recurrence").
			Values(reminderRecurrenceValues...).
			Optional().
			Nillable(),
		// NULL = pending.
		field.Time("done_at").Optional().Nillable(),
		field.String("created_by_actor_id").Optional().Nillable(),
	}
}
func (Reminder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "due_at"),
	}
}
