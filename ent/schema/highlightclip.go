package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type HighlightClip struct {
	ent.Schema
}

func (HighlightClip) Fields() []ent.Field {
	return []ent.Field{
		field.Int("highlight_index"),
		field.String("role"),
		field.String("algorithm_version"),
		field.Enum("status").Values("AVAILABLE", "FAILED", "SKIPPED").Default("AVAILABLE"),
		field.Int("priority").Default(0),
		field.Float("start_seconds"),
		field.Float("end_seconds"),
		field.Float("peak_seconds"),
		field.String("source_path"),
		field.String("output_dir"),
		field.String("title").Optional().Nillable(),
		field.Text("description").Optional().Nillable(),
		field.JSON("tags", []string{}).Optional(),
		field.Float("score").Default(0),
		field.Text("model_payload").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (HighlightClip) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("match_round", MatchRound.Type).Ref("highlight_clips").Unique().Required(),
		edge.To("bilibili_publications", BilibiliHighlightPublication.Type),
	}
}

func (HighlightClip) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("match_round").Fields("role", "algorithm_version", "highlight_index").Unique(),
		index.Fields("status"),
	}
}
