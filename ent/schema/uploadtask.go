package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type UploadTask struct {
	ent.Schema
}

func (UploadTask) Fields() []ent.Field {
	return []ent.Field{
		field.String("source_path"),
		field.Enum("status").Values("PENDING", "DISPATCHING", "RUNNING", "SUCCEEDED", "FAILED").Default("PENDING"),
		field.String("k8s_job_name").Optional().Nillable(),
		field.Int("attempts").Default(0),
		field.String("file_token").Optional().Nillable(),
		field.String("file_url").Optional().Nillable(),
		field.String("error_message").Optional().Nillable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("lark_replied_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (UploadTask) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("record_task", RecordTask.Type).Ref("upload_task").Unique().Required(),
		edge.From("source_artifact", MediaArtifact.Type).Ref("upload_task").Unique(),
	}
}

func (UploadTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("record_task").Unique(),
		index.Edges("source_artifact").Unique(),
		index.Fields("status"),
	}
}
