package kubejob

import (
	"context"
	"path/filepath"

	"github.com/pkg/errors"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	"scutbot.cn/web/rm-monitor/pkg/config"
)

type Client struct {
	rest *rest.RESTClient
}

func NewInClusterClient() (*Client, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, errors.Wrap(err, "load in-cluster k8s config")
	}
	scheme := runtime.NewScheme()
	if err := batchv1.AddToScheme(scheme); err != nil {
		return nil, errors.Wrap(err, "add batch scheme")
	}
	cfg := rest.CopyConfig(restConfig)
	cfg.APIPath = "/apis"
	cfg.GroupVersion = &schema.GroupVersion{Group: "batch", Version: "v1"}
	cfg.NegotiatedSerializer = serializer.NewCodecFactory(scheme).WithoutConversion()
	client, err := rest.RESTClientFor(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "create k8s rest client")
	}
	return &Client{rest: client}, nil
}

func (c *Client) CreateJob(ctx context.Context, namespace string, job *batchv1.Job) error {
	err := c.rest.Post().
		Namespace(namespace).
		Resource("jobs").
		Body(job).
		Do(ctx).
		Error()
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return errors.Wrap(err, "create k8s job")
}

func (c *Client) JobExists(ctx context.Context, namespace, name string) (bool, error) {
	var job batchv1.Job
	err := c.rest.Get().
		Namespace(namespace).
		Resource("jobs").
		Name(name).
		Do(ctx).
		Into(&job)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, errors.Wrap(err, "get k8s job")
	}
	return true, nil
}

type JobSpec struct {
	Name     string
	App      string
	Image    string
	Command  []string
	Args     []string
	Env      map[string]string
	MountPVC bool
	WorkDir  string
	CPU      string
	Memory   string
	CPULimit string
	MemLimit string
}

func Build(conf config.K8sJobConf, spec JobSpec) *batchv1.Job {
	conf = conf.WithDefaults()
	labels := map[string]string{"app.kubernetes.io/name": spec.App, "rm-monitor/job": spec.App}
	env := make([]corev1.EnvVar, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}
	container := corev1.Container{
		Name:            spec.App,
		Image:           spec.Image,
		Command:         spec.Command,
		Args:            spec.Args,
		Env:             env,
		ImagePullPolicy: corev1.PullAlways,
	}
	if spec.WorkDir != "" {
		container.WorkingDir = spec.WorkDir
	}
	if spec.CPU != "" || spec.Memory != "" {
		container.Resources.Requests = corev1.ResourceList{}
		if spec.CPU != "" {
			container.Resources.Requests[corev1.ResourceCPU] = resource.MustParse(spec.CPU)
		}
		if spec.Memory != "" {
			container.Resources.Requests[corev1.ResourceMemory] = resource.MustParse(spec.Memory)
		}
	}
	if spec.CPULimit != "" || spec.MemLimit != "" {
		container.Resources.Limits = corev1.ResourceList{}
		if spec.CPULimit != "" {
			container.Resources.Limits[corev1.ResourceCPU] = resource.MustParse(spec.CPULimit)
		}
		if spec.MemLimit != "" {
			container.Resources.Limits[corev1.ResourceMemory] = resource.MustParse(spec.MemLimit)
		}
	}
	volumes := []corev1.Volume{}
	if conf.ConfigMapName != "" {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "config",
			MountPath: "/app/etc/config.yml",
			SubPath:   "config.yml",
			ReadOnly:  true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: conf.ConfigMapName},
				},
			},
		})
	}
	if spec.MountPVC {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "records",
			MountPath: filepath.ToSlash(conf.RecordsMountPath),
		})
		volumes = append(volumes, corev1.Volume{
			Name: "records",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: conf.RecordsPVC},
			},
		})
	}
	return &batchv1.Job{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: conf.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &conf.BackoffLimit,
			TTLSecondsAfterFinished: &conf.TTLSecondsAfterFinished,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: conf.ServiceAccountName,
					NodeSelector: map[string]string{
						conf.StorageNodeSelectorKey: conf.StorageNodeSelectorValue,
					},
					Containers: []corev1.Container{container},
					Volumes:    volumes,
				},
			},
		},
	}
}
