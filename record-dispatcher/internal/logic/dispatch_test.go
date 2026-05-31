package logic

import (
	"testing"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
)

func TestFilterBlacklistedRoles(t *testing.T) {
	urls := map[string]string{
		"主视角":         "main",
		"主视角（无解说版）":   "main-no-commentary",
		"蓝方机器人第一视角合集": "blue-all",
		"红方英雄第一视角":    "red-hero",
	}
	got := filterBlacklistedRoles(urls, []string{"主视角（无解说版）", "蓝方机器人第一视角合集"})

	if _, ok := got["主视角（无解说版）"]; ok {
		t.Fatal("blacklisted main no-commentary role was kept")
	}
	if _, ok := got["蓝方机器人第一视角合集"]; ok {
		t.Fatal("blacklisted blue all role was kept")
	}
	if got["主视角"] != "main" || got["红方英雄第一视角"] != "red-hero" {
		t.Fatalf("non-blacklisted roles changed: %#v", got)
	}
}

func TestManifestJobNameStablePerMatch(t *testing.T) {
	first := manifestJobName("match-1")
	if first != manifestJobName("match-1") {
		t.Fatal("manifest job name must be stable for the same match")
	}
	if first == manifestJobName("match-2") {
		t.Fatal("manifest job name should include match id")
	}
}

func TestCompletedMatchRequiresAllRoundsEnded(t *testing.T) {
	if completedMatch(&ent.Match{}) {
		t.Fatal("match without rounds should not be complete")
	}
	m := &ent.Match{Edges: ent.MatchEdges{Rounds: []*ent.MatchRound{
		{Status: matchround.StatusENDED},
		{Status: matchround.StatusSTARTED},
	}}}
	if completedMatch(m) {
		t.Fatal("match with started round should not be complete")
	}
	m.Edges.Rounds[1].Status = matchround.StatusENDED
	if !completedMatch(m) {
		t.Fatal("match with all rounds ended should be complete")
	}
}

func TestSTTJobSpecIsSingleContainer(t *testing.T) {
	jobConf := common.K8sJobConf{
		Namespace:        "rm-monitor",
		Image:            "example/stt-job:test",
		ConfigMapName:    "stt-job-config",
		RecordsPVC:       "records",
		RecordsMountPath: "/records",
		ImagePullPolicy:  "IfNotPresent",
	}
	job := kubejob.Build(jobConf, kubejob.JobSpec{
		Name:  "stt-1",
		App:   "stt-job",
		Image: jobConf.Image,
		Env:   map[string]string{jobcontract.EnvName: `{"source_path":"/records/round/主视角.flv"}`},
	})
	containers := job.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("containers = %d, want 1", len(containers))
	}
	if containers[0].Name != "stt-job" {
		t.Fatalf("container name = %q", containers[0].Name)
	}
	if containers[0].Env[0].Name != jobcontract.EnvName {
		t.Fatalf("stt env = %#v", containers[0].Env)
	}
	if len(containers[0].Args) != 0 {
		t.Fatalf("stt args = %#v, want default entrypoint without mode", containers[0].Args)
	}
}
