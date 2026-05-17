package kubejob

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"scutbot.cn/web/rm-monitor/pkg/config"
)

func TestBuildTranscodeWebDAVPVCUsesAvoidAffinityWithoutStorageSelector(t *testing.T) {
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
		AvoidNodeLabelKey:          "rm-monitor/record",
		DisableStorageNodeSelector: true,
	})
	pod := job.Spec.Template.Spec
	if len(pod.NodeSelector) != 0 {
		t.Fatalf("NodeSelector = %#v, want empty", pod.NodeSelector)
	}
	if pod.Affinity == nil || pod.Affinity.NodeAffinity == nil || pod.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		t.Fatalf("missing required node affinity")
	}
	req := pod.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0]
	if req.Key != "rm-monitor/record" || req.Operator != corev1.NodeSelectorOpDoesNotExist {
		t.Fatalf("affinity requirement = %#v", req)
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
		Name:     "record-1",
		App:      "record-job",
		Image:    "example/record-job:test",
		MountPVC: true,
	})
	pod := job.Spec.Template.Spec
	if got := pod.NodeSelector["rm-monitor/record"]; got != "true" {
		t.Fatalf("storage node selector = %q, want true", got)
	}
	if len(pod.Volumes) != 2 {
		t.Fatalf("volumes = %#v, want config + records", pod.Volumes)
	}
}
