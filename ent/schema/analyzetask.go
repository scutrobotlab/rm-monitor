package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type AnalyzeTask struct {
	ent.Schema
}

func (AnalyzeTask) Fields() []ent.Field {
	return []ent.Field{
		field.String("role"),
		field.Enum("status").Values("PENDING", "DISPATCHING", "RUNNING", "SUCCEEDED", "FAILED").Default("PENDING"),
		field.Int("priority").Default(0),
		field.String("k8s_job_name").Optional().Nillable(),
		field.Int("attempts").Default(0),
		field.String("round_json_path").Optional().Nillable(),
		field.String("settlement_image_path").Optional().Nillable(),
		field.Enum("settlement_status").Values("CONFIRMED", "INVALID").Optional().Nillable(),
		field.Float("effective_start_seconds").Optional().Nillable(),
		field.Float("effective_end_seconds").Optional().Nillable(),
		field.String("error_message").Optional().Nillable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (AnalyzeTask) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("match_round", MatchRound.Type).Ref("analyze_tasks").Unique().Required(),
		edge.From("source_artifact", MediaArtifact.Type).Ref("analyze_tasks").Unique().Required(),
	}
}

func (AnalyzeTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("match_round").Fields("role").Unique(),
		index.Fields("status"),
		index.Fields("status", "priority", "created_at"),
		index.Fields("status", "updated_at"),
	}
}
