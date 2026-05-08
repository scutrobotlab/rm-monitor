package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MediaArtifact struct {
	ent.Schema
}

func (MediaArtifact) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("kind").Values("source", "archive"),
		field.String("path"),
		field.Enum("format").Values("flv", "mp4"),
		field.Enum("codec").Values("copy", "av1"),
		field.Int64("file_size").Optional().Nillable(),
		field.String("checksum").Optional().Nillable(),
		field.Enum("status").Values("AVAILABLE", "DELETED").Default("AVAILABLE"),
		field.Time("deletable_at").Optional().Nillable(),
		field.Time("deleted_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (MediaArtifact) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("record_task", RecordTask.Type).Ref("media_artifacts").Unique().Required(),
		edge.To("upload_task", UploadTask.Type).Unique(),
		edge.To("source_transcode_task", TranscodeTask.Type).Unique(),
		edge.To("archive_transcode_task", TranscodeTask.Type).Unique(),
	}
}

func (MediaArtifact) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("record_task").Fields("kind").Unique(),
		index.Fields("status"),
		index.Fields("kind", "format", "codec"),
	}
}
