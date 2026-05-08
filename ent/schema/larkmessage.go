package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type LarkMessage struct {
	ent.Schema
}

func (LarkMessage) Fields() []ent.Field {
	return []ent.Field{
		field.String("message_id").Unique(),
		field.JSON("card_payload", map[string]any{}).Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (LarkMessage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("match", Match.Type).Ref("lark_messages").Unique().Required(),
	}
}
