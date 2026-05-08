package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type Team struct {
	ent.Schema
}

func (Team) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Unique().Immutable(),
		field.String("name").Default(""),
		field.String("school_name").Default(""),
		field.String("school_logo").Default(""),
		field.JSON("raw_payload", map[string]any{}).Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (Team) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("red_matches", Match.Type),
		edge.To("blue_matches", Match.Type),
	}
}
