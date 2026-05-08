package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type TranscodeTask struct {
	ent.Schema
}

func (TranscodeTask) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("status").Values("PENDING", "DISPATCHING", "RUNNING", "SUCCEEDED", "FAILED").Default("PENDING"),
		field.String("k8s_job_name").Optional().Nillable(),
		field.Int("attempts").Default(0),
		field.String("error_message").Optional().Nillable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (TranscodeTask) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("source_artifact", MediaArtifact.Type).Ref("source_transcode_task").Unique().Required(),
		edge.From("archive_artifact", MediaArtifact.Type).Ref("archive_transcode_task").Unique(),
	}
}

func (TranscodeTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("source_artifact").Unique(),
		index.Fields("status"),
	}
}
