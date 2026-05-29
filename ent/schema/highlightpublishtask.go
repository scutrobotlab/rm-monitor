package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type HighlightPublishTask struct {
	ent.Schema
}

func (HighlightPublishTask) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("platform").Values("bilibili"),
		field.Enum("status").Values("PENDING", "DISPATCHING", "RUNNING", "SUCCEEDED", "FAILED").Default("PENDING"),
		field.Int("priority").Default(0),
		field.String("k8s_job_name").Optional().Nillable(),
		field.Int("attempts").Default(0),
		field.String("publish_url").Optional().Nillable(),
		field.String("external_id").Optional().Nillable(),
		field.String("error_message").Optional().Nillable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (HighlightPublishTask) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("highlight_clip", HighlightClip.Type).Ref("publish_tasks").Unique().Required(),
	}
}

func (HighlightPublishTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("highlight_clip").Fields("platform").Unique(),
		index.Fields("status"),
		index.Fields("status", "priority", "created_at"),
		index.Fields("status", "updated_at"),
	}
}
