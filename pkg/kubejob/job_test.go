package kubejob

import (
	"testing"

	"scutbot.cn/web/rm-monitor/pkg/config"
)

func TestBuildSharedPVCUsesNoNodeSelector(t *testing.T) {
	job := Build(config.K8sJobConf{
		Namespace:        "rm-monitor",
		Image:            "example/transcode-job:test",
		ConfigMapName:    "transcode-job-config",
		RecordsPVC:       "shared-records",
		RecordsMountPath: "/records",
	}, JobSpec{
		Name:              "transcode-1",
		App:               "transcode-job",
		Image:             "example/transcode-job:test",
		PriorityClassName: PriorityClassBackground,
		SpreadByHostname:  true,
	})
	pod := job.Spec.Template.Spec
	if len(pod.NodeSelector) != 0 {
		t.Fatalf("NodeSelector = %#v, want empty", pod.NodeSelector)
	}
	if pod.Affinity != nil {
		t.Fatalf("Affinity = %#v, want nil", pod.Affinity)
	}
	if got := pod.PriorityClassName; got != PriorityClassBackground {
		t.Fatalf("priority class = %q, want %s", got, PriorityClassBackground)
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
	if got := pod.Volumes[1].PersistentVolumeClaim.ClaimName; got != "shared-records" {
		t.Fatalf("records pvc = %q, want shared-records", got)
	}
}

func TestBuildWithPVCDoesNotInferNodeSelector(t *testing.T) {
	job := Build(config.K8sJobConf{
		Namespace:        "rm-monitor",
		Image:            "example/record-job:test",
		ConfigMapName:    "record-job-config",
		RecordsPVC:       "records",
		RecordsMountPath: "/records",
	}, JobSpec{
		Name:              "record-1",
		App:               "record-job",
		Image:             "example/record-job:test",
		PriorityClassName: PriorityClassRecordCritical,
	})
	pod := job.Spec.Template.Spec
	if len(pod.NodeSelector) != 0 {
		t.Fatalf("node selector = %#v, want empty", pod.NodeSelector)
	}
	if got := pod.PriorityClassName; got != PriorityClassRecordCritical {
		t.Fatalf("priority class = %q, want %s", got, PriorityClassRecordCritical)
	}
	if len(pod.Volumes) != 2 {
		t.Fatalf("volumes = %#v, want config + records", pod.Volumes)
	}
}

func TestBuildWithExtraContainerMountsSharedVolumes(t *testing.T) {
	job := Build(config.K8sJobConf{
		Namespace:        "rm-monitor",
		Image:            "example/stt-job:test",
		ConfigMapName:    "stt-job-config",
		RecordsPVC:       "records",
		RecordsMountPath: "/records",
	}, JobSpec{
		Name:          "stt-1",
		App:           "stt-job",
		ContainerName: "audio-recorder",
		Image:         "example/stt-job:test",
		Args:          []string{"-mode", "audio-recorder"},
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
	if len(pod.NodeSelector) != 0 {
		t.Fatalf("node selector = %#v, want empty", pod.NodeSelector)
	}
}
