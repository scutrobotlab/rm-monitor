package kubejob

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"scutbot.cn/web/rm-monitor/pkg/config"
)

func TestBuildWithoutPVCUsesAvoidAffinity(t *testing.T) {
	job := Build(config.K8sJobConf{
		Namespace:                "rm-monitor",
		Image:                    "example/transcode-job:test",
		ConfigMapName:            "transcode-job-config",
		StorageNodeSelectorKey:   "rm-monitor/record",
		StorageNodeSelectorValue: "true",
	}, JobSpec{
		Name:              "transcode-1",
		App:               "transcode-job",
		Image:             "example/transcode-job:test",
		MountPVC:          false,
		AvoidNodeLabelKey: "rm-monitor/record",
		SecretEnv: map[string]corev1.SecretKeySelector{
			"RCLONE_WEBDAV_USER": {LocalObjectReference: corev1.LocalObjectReference{Name: "secret"}, Key: "username"},
		},
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
	if len(pod.Volumes) != 1 || pod.Volumes[0].Name != "config" {
		t.Fatalf("volumes = %#v, want only config volume", pod.Volumes)
	}
	env := pod.Containers[0].Env
	if len(env) != 1 || env[0].ValueFrom == nil || env[0].ValueFrom.SecretKeyRef == nil {
		t.Fatalf("secret env not configured: %#v", env)
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
