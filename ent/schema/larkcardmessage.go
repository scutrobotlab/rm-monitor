package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type LarkCardMessage struct {
	ent.Schema
}

func (LarkCardMessage) Fields() []ent.Field {
	return []ent.Field{
		field.String("message_id").Unique(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (LarkCardMessage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("card", LarkMessage.Type).Ref("card_messages").Unique().Required(),
	}
}
