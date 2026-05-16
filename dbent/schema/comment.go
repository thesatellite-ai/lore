package schema

import (
	"saas/pkg/aicoder/ids"

	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/khanakia/entx/enttui"
)

// Comment: free-form discussion threads on entities (R22 #32).
// NOT in audit hash chain — discussion, not facts. Append-only but
// separate integrity model.
type Comment struct{ ent.Schema }

func (Comment) Mixin() []ent.Mixin {
	return []ent.Mixin{
		AicoderBaseMixin{Prefix: ids.PrefixComment},
	}
}
func (Comment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		enttui.AllowCreate{},
		enttui.AllowDelete{},
	}
}
func (Comment) Fields() []ent.Field {
	return []ent.Field{
		// Polymorphic target.
		field.String("entity_table").NotEmpty().
			Annotations(enttui.Editable{}),
		field.String("entity_id").NotEmpty().Match(idValidatorRE).
			Annotations(enttui.Editable{}),
		field.Text("body").NotEmpty().
			Annotations(enttui.Editable{}),
		field.String("created_by_actor_id").Optional().Nillable().
			Annotations(enttui.Editable{}),
	}
}
func (Comment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("entity_table", "entity_id"),
	}
}
