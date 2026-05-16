package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

// BaseApp to be shared will all different schemas.
type BaseApp struct {
	mixin.Schema
}

// Fields of the BaseApp.
func (cls BaseApp) Fields() []ent.Field {
	return []ent.Field{

		field.String("app_id").Optional().Annotations(
			entgql.Skip(entgql.SkipMutationCreateInput, entgql.SkipMutationCreateInput),
		),
	}
}
