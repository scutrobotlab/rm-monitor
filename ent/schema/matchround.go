package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MatchRound struct {
	ent.Schema
}

func (MatchRound) Fields() []ent.Field {
	return []ent.Field{
		field.Int("round_no"),
		field.Enum("status").Values("STARTED", "ENDED"),
		field.Enum("winner").Values("blue", "red", "draw").Optional().Nillable(),
		field.Time("started_at").Default(time.Now),
		field.Time("ended_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (MatchRound) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("match", Match.Type).Ref("rounds").Unique().Required(),
		edge.To("record_tasks", RecordTask.Type),
		edge.To("highlight_clips", HighlightClip.Type),
		edge.To("ocr_tasks", OCRTask.Type),
	}
}

func (MatchRound) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("match").Fields("round_no").Unique(),
		index.Fields("status"),
		index.Fields("updated_at"),
		index.Fields("status", "updated_at"),
	}
}
