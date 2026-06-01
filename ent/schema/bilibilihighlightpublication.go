package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type BilibiliHighlightPublication struct {
	ent.Schema
}

func (BilibiliHighlightPublication) Fields() []ent.Field {
	return []ent.Field{
		field.String("external_id").Optional().Nillable(),
		field.String("url").Optional().Nillable(),
		field.JSON("payload", map[string]any{}).Optional(),
		field.Text("error_message").Optional().Nillable(),
		field.Time("published_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (BilibiliHighlightPublication) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("highlight_clip", HighlightClip.Type).Ref("bilibili_publications").Unique().Required(),
	}
}

func (BilibiliHighlightPublication) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("highlight_clip").Unique(),
		index.Fields("updated_at"),
	}
}
