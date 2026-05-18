package kubejob

import (
	"testing"

	"scutbot.cn/web/rm-monitor/pkg/config"
)

func TestBuildTranscodeWebDAVPVCUsesNoStorageSelector(t *testing.T) {
	job := Build(config.K8sJobConf{
		Namespace:                "rm-monitor",
		Image:                    "example/transcode-job:test",
		ConfigMapName:            "transcode-job-config",
		StorageNodeSelectorKey:   "rm-monitor/record",
		StorageNodeSelectorValue: "true",
		RecordsPVC:               "local-records",
		RecordsMountPath:         "/records",
	}, JobSpec{
		Name:                       "transcode-1",
		App:                        "transcode-job",
		Image:                      "example/transcode-job:test",
		MountPVC:                   true,
		RecordsPVC:                 "webdav-records",
		DisableStorageNodeSelector: true,
		PriorityClassName:          "rm-monitor-background",
		PreferAvoidNodeLabelKey:    "rm-monitor/record",
		PreferAvoidNodeLabelValue:  "true",
		SpreadByHostname:           true,
	})
	pod := job.Spec.Template.Spec
	if len(pod.NodeSelector) != 0 {
		t.Fatalf("NodeSelector = %#v, want empty", pod.NodeSelector)
	}
	if pod.Affinity == nil || pod.Affinity.NodeAffinity == nil {
		t.Fatalf("Affinity = %#v, want preferred node affinity", pod.Affinity)
	}
	preferred := pod.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	if len(preferred) != 1 {
		t.Fatalf("preferred node affinity = %#v, want 1 term", preferred)
	}
	exprs := preferred[0].Preference.MatchExpressions
	if len(exprs) != 1 || exprs[0].Key != "rm-monitor/record" || exprs[0].Operator != "NotIn" || len(exprs[0].Values) != 1 || exprs[0].Values[0] != "true" {
		t.Fatalf("preferred expression = %#v, want rm-monitor/record NotIn true", exprs)
	}
	if got := pod.PriorityClassName; got != "rm-monitor-background" {
		t.Fatalf("priority class = %q, want rm-monitor-background", got)
	}
	if len(pod.TopologySpreadConstraints) != 1 {
		t.Fatalf("topology spread = %#v, want 1 constraint", pod.TopologySpreadConstraints)
	}
	spread := pod.TopologySpreadConstraints[0]
	if spread.TopologyKey != "kubernetes.io/hostname" || spread.WhenUnsatisfiable != "ScheduleAnyway" {
		t.Fatalf("topology spread = %#v, want hostname ScheduleAnyway", spread)
	}
	if len(pod.Volumes) != 2 || pod.Volumes[1].Name != "records" {
		t.Fatalf("volumes = %#v, want config + records", pod.Volumes)
	}
	if got := pod.Volumes[1].PersistentVolumeClaim.ClaimName; got != "webdav-records" {
		t.Fatalf("records pvc = %q, want webdav-records", got)
	}
}

func TestBuildWithPVCUsesStorageNodeSelector(t *testing.T) {
	job := Build(config.K8sJobConf{
		Namespace:                "rm-monitor",
		Image:                    "example/record-job:test",
		ConfigMapName:            "record-job-config",
		StorageNodeSelectorKey:   "rm-monitor/record",
		StorageNodeSelectorValue: "true",
		RecordsPVC:               "records",
		RecordsMountPath:         "/records",
	}, JobSpec{
		Name:              "record-1",
		App:               "record-job",
		Image:             "example/record-job:test",
		MountPVC:          true,
		PriorityClassName: "rm-monitor-record-critical",
	})
	pod := job.Spec.Template.Spec
	if got := pod.NodeSelector["rm-monitor/record"]; got != "true" {
		t.Fatalf("storage node selector = %q, want true", got)
	}
	if got := pod.PriorityClassName; got != "rm-monitor-record-critical" {
		t.Fatalf("priority class = %q, want rm-monitor-record-critical", got)
	}
	if len(pod.Volumes) != 2 {
		t.Fatalf("volumes = %#v, want config + records", pod.Volumes)
	}
}

func TestBuildWithExtraContainerMountsSharedVolumes(t *testing.T) {
	job := Build(config.K8sJobConf{
		Namespace:                "rm-monitor",
		Image:                    "example/stt-job:test",
		ConfigMapName:            "stt-job-config",
		StorageNodeSelectorKey:   "rm-monitor/record",
		StorageNodeSelectorValue: "true",
		RecordsPVC:               "records",
		RecordsMountPath:         "/records",
	}, JobSpec{
		Name:          "stt-1",
		App:           "stt-job",
		ContainerName: "audio-recorder",
		Image:         "example/stt-job:test",
		Args:          []string{"-mode", "audio-recorder"},
		MountPVC:      true,
		ExtraContainers: []ContainerSpec{
			{Name: "recognizer", Image: "example/stt-job:test", Args: []string{"-mode", "recognizer"}},
		},
	})
	pod := job.Spec.Template.Spec
	if len(pod.Containers) != 2 {
		t.Fatalf("containers = %d, want 2", len(pod.Containers))
	}
	if pod.Containers[0].Name != "audio-recorder" || pod.Containers[1].Name != "recognizer" {
		t.Fatalf("container names = %q, %q", pod.Containers[0].Name, pod.Containers[1].Name)
	}
	for _, c := range pod.Containers {
		if len(c.VolumeMounts) != 2 {
			t.Fatalf("%s mounts = %#v, want config + records", c.Name, c.VolumeMounts)
		}
	}
	if got := pod.NodeSelector["rm-monitor/record"]; got != "true" {
		t.Fatalf("storage node selector = %q, want true", got)
	}
}
