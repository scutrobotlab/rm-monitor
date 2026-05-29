package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Match struct {
	ent.Schema
}

func (Match) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Unique().Immutable(),
		field.String("event"),
		field.String("zone"),
		field.Int("order"),
		field.String("match_type").Default(""),
		field.String("match_slug").Optional().Nillable(),
		field.Int("total_rounds").Default(0),
		field.Int("priority").Default(0),
		field.Enum("result").Values("RED", "BLUE", "DRAW", "UNKNOWN").Default("UNKNOWN"),
		field.String("winner_placeholder_name").Optional().Nillable(),
		field.String("loser_placeholder_name").Optional().Nillable(),
		field.String("latest_status").Default(""),
		field.Text("report").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (Match) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("red_team", Team.Type).Ref("red_matches").Unique().Required(),
		edge.From("blue_team", Team.Type).Ref("blue_matches").Unique().Required(),
		edge.To("rounds", MatchRound.Type),
		edge.To("lark_messages", LarkMessage.Type),
	}
}

func (Match) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("updated_at"),
		index.Fields("latest_status", "updated_at"),
	}
}
