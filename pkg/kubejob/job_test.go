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
	})
	pod := job.Spec.Template.Spec
	if len(pod.NodeSelector) != 0 {
		t.Fatalf("NodeSelector = %#v, want empty", pod.NodeSelector)
	}
	if pod.Affinity != nil {
		t.Fatalf("Affinity = %#v, want nil", pod.Affinity)
	}
	if got := pod.PriorityClassName; got != "rm-monitor-background" {
		t.Fatalf("priority class = %q, want rm-monitor-background", got)
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
