package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type LarkBitableRecord struct {
	ent.Schema
}

func (LarkBitableRecord) Fields() []ent.Field {
	return []ent.Field{
		field.String("role"),
		field.String("app_token"),
		field.String("table_id"),
		field.String("record_id"),
		field.String("record_url").Optional().Nillable(),
		field.String("attachment_file_token").Optional().Nillable(),
		field.String("source_path"),
		field.Int64("file_size").Default(0),
		field.Time("source_deleted_at").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (LarkBitableRecord) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("match_round", MatchRound.Type).Ref("lark_bitable_records").Unique().Required(),
	}
}

func (LarkBitableRecord) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("match_round").Fields("role").Unique(),
		index.Fields("updated_at"),
	}
}
