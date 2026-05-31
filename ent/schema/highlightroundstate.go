package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type HighlightRoundState struct {
	ent.Schema
}

func (HighlightRoundState) Fields() []ent.Field {
	return []ent.Field{
		field.String("role"),
		field.String("algorithm_version"),
		field.Enum("status").Values("PENDING", "COMPLETED").Default("PENDING"),
		field.Int("candidate_count").Default(0),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (HighlightRoundState) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("match_round", MatchRound.Type).Ref("highlight_states").Unique().Required(),
	}
}

func (HighlightRoundState) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("match_round").Fields("role", "algorithm_version").Unique(),
		index.Fields("status"),
		index.Fields("updated_at"),
	}
}
