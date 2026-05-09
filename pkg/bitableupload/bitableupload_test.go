package bitableupload

import (
	"testing"

	"scutbot.cn/web/rm-monitor/ent"
)

func TestTableName(t *testing.T) {
	if got := TableName("RoboMaster", "南部"); got != "RoboMaster-南部" {
		t.Fatalf("TableName() = %q", got)
	}
}

func TestRecordFields(t *testing.T) {
	m := &ent.Match{
		Order:     3,
		MatchType: "bo3",
		Edges: ent.MatchEdges{
			RedTeam:  &ent.Team{Name: "红队", SchoolName: "红校"},
			BlueTeam: &ent.Team{Name: "蓝队", SchoolName: "蓝校"},
		},
	}
	fields := RecordFields(m, "big")
	if fields[FieldRole] != "big" {
		t.Fatalf("role = %v", fields[FieldRole])
	}
	if fields[FieldMatch] != "3. 红校-红队 VS 蓝校-蓝队" {
		t.Fatalf("match = %v", fields[FieldMatch])
	}
	if fields[FieldType] != "bo3" || fields[FieldRedTeam] != "红校-红队" || fields[FieldBlueTeam] != "蓝校-蓝队" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
}

func TestAttachmentValue(t *testing.T) {
	value := AttachmentValue("boxabc", "source.flv")
	if len(value) != 1 || value[0]["file_token"] != "boxabc" || value[0]["name"] != "source.flv" {
		t.Fatalf("unexpected attachment value: %#v", value)
	}
}
