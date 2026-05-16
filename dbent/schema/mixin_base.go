package schema

import (
	"time"

	gonanoid "github.com/matoous/go-nanoid/v2"

	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

// BaseMixin to be shared will all different schemas.
type BaseMixin struct {
	mixin.Schema
	Prefix string
	Length int
}

// Fields of the BaseMixin.
func (cls BaseMixin) Fields() []ent.Field {
	prefix := "u"
	if len(cls.Prefix) > 0 {
		prefix = cls.Prefix + "_"
	}

	var length int = 17
	if cls.Length > 17 {
		length = cls.Length
	}

	return []ent.Field{
		field.String("id").
			DefaultFunc(func() string {
				return prefix + gonanoid.MustGenerate("0123456789abcdefghijklmnopqrstuvwxyz", length)
			}).Unique(),

		field.Time("created_at").
			Optional().
			Default(time.Now).
			Immutable().
			Annotations(
				entgql.OrderField("CREATED_AT"),
				SkipMutations,
			),

		field.Time("updated_at").
			Optional().
			Default(time.Now).
			UpdateDefault(time.Now).Annotations(SkipMutations),
	}
}

var SkipMutations = entgql.Skip(entgql.SkipMutationCreateInput, entgql.SkipMutationUpdateInput)
var SkipAll = entgql.Skip(entgql.SkipAll)
