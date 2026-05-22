package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type RecordTask struct {
	ent.Schema
}

func (RecordTask) Fields() []ent.Field {
	return []ent.Field{
		field.String("role"),
		field.String("source_url"),
		field.String("output_path"),
		field.Enum("status").Values("PENDING", "DISPATCHING", "RUNNING", "SUCCEEDED", "FAILED", "CANCEL_REQUESTED", "CANCELED").Default("PENDING"),
		field.String("k8s_job_name").Optional().Nillable(),
		field.Int("attempts").Default(0),
		field.Int("priority").Default(0),
		field.Int64("file_size").Optional().Nillable(),
		field.String("checksum").Optional().Nillable(),
		field.String("error_message").Optional().Nillable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (RecordTask) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("match_round", MatchRound.Type).Ref("record_tasks").Unique().Required(),
		edge.To("upload_task", UploadTask.Type).Unique(),
		edge.To("media_artifacts", MediaArtifact.Type),
	}
}

func (RecordTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("match_round").Fields("role").Unique(),
		index.Fields("status"),
		index.Fields("status", "priority", "created_at"),
	}
}
