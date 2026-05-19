package logic

import (
	"testing"

	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	common "scutbot.cn/web/rm-monitor/pkg/config"
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

func TestSTTJobSpecCarriesSourceURL(t *testing.T) {
	jobConf := common.K8sJobConf{
		Namespace:        "rm-monitor",
		Image:            "example/stt-job:test",
		ConfigMapName:    "stt-job-config",
		RecordsPVC:       "records",
		RecordsMountPath: "/records",
		ImagePullPolicy:  "IfNotPresent",
	}
	job := kubejob.Build(jobConf, kubejob.JobSpec{
		Name:          "stt-1",
		App:           "stt-job",
		ContainerName: "audio-recorder",
		Image:         jobConf.Image,
		Env:           map[string]string{"STT_SOURCE_URL": "https://example.test/live.m3u8"},
		ExtraContainers: []kubejob.ContainerSpec{
			{Name: "recognizer", Image: jobConf.Image},
		},
	})
	containers := job.Spec.Template.Spec.Containers
	if len(containers) != 2 {
		t.Fatalf("containers = %d, want 2", len(containers))
	}
	if containers[0].Env[0].Name != "STT_SOURCE_URL" || containers[0].Env[0].Value != "https://example.test/live.m3u8" {
		t.Fatalf("audio recorder env = %#v", containers[0].Env)
	}
	if len(containers[1].Env) != 0 {
		t.Fatalf("recognizer should not receive source url env, got %#v", containers[1].Env)
	}
}
