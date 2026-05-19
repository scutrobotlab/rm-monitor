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

const (
	PriorityClassRecordCritical = "record-critical"
	PriorityClassBackground     = "background"
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

func (c *Client) CountUnfinishedJobs(ctx context.Context, namespace, labelSelector string) (int, error) {
	var jobs batchv1.JobList
	err := c.rest.Get().
		Namespace(namespace).
		Resource("jobs").
		Param("labelSelector", labelSelector).
		Do(ctx).
		Into(&jobs)
	if err != nil {
		return 0, errors.Wrap(err, "list k8s jobs")
	}
	count := 0
	for _, job := range jobs.Items {
		if jobFinished(job) {
			continue
		}
		count++
	}
	return count, nil
}

func jobFinished(job batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		if cond.Type == batchv1.JobComplete || cond.Type == batchv1.JobFailed {
			return true
		}
	}
	return false
}

type JobSpec struct {
	Name              string
	App               string
	ContainerName     string
	Image             string
	Command           []string
	Args              []string
	Env               map[string]string
	SecretEnv         map[string]corev1.SecretKeySelector
	WorkDir           string
	CPU               string
	Memory            string
	CPULimit          string
	MemLimit          string
	PriorityClassName string
	SpreadByHostname  bool
	ExtraContainers   []ContainerSpec
}

type ContainerSpec struct {
	Name      string
	Image     string
	Command   []string
	Args      []string
	Env       map[string]string
	SecretEnv map[string]corev1.SecretKeySelector
	WorkDir   string
	CPU       string
	Memory    string
	CPULimit  string
	MemLimit  string
}

func Build(conf config.K8sJobConf, spec JobSpec) *batchv1.Job {
	conf = conf.WithDefaults()
	labels := map[string]string{"app.kubernetes.io/name": spec.App, "rm-monitor/job": spec.App}
	containerName := spec.App
	if spec.ContainerName != "" {
		containerName = spec.ContainerName
	}
	volumeMounts := []corev1.VolumeMount{}
	volumes := []corev1.Volume{}
	if conf.ConfigMapName != "" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "config",
			MountPath: "/etc/rm-monitor/config.yml",
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
	if conf.RecordsPVC != "" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
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
	container := buildContainer(conf, ContainerSpec{
		Name:      containerName,
		Image:     spec.Image,
		Command:   spec.Command,
		Args:      spec.Args,
		Env:       spec.Env,
		SecretEnv: spec.SecretEnv,
		WorkDir:   spec.WorkDir,
		CPU:       spec.CPU,
		Memory:    spec.Memory,
		CPULimit:  spec.CPULimit,
		MemLimit:  spec.MemLimit,
	}, volumeMounts)
	containers := []corev1.Container{container}
	for _, extra := range spec.ExtraContainers {
		containers = append(containers, buildContainer(conf, extra, volumeMounts))
	}
	podSpec := corev1.PodSpec{
		RestartPolicy:      corev1.RestartPolicyNever,
		ServiceAccountName: conf.ServiceAccountName,
		Containers:         containers,
		Volumes:            volumes,
	}
	if spec.PriorityClassName != "" {
		podSpec.PriorityClassName = spec.PriorityClassName
	}
	if spec.SpreadByHostname {
		podSpec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{
			{
				MaxSkew:           1,
				TopologyKey:       corev1.LabelHostname,
				WhenUnsatisfiable: corev1.ScheduleAnyway,
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: labels,
				},
			},
		}
	}
	return &batchv1.Job{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: conf.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &conf.BackoffLimit,
			TTLSecondsAfterFinished: &conf.TTLSecondsAfterFinished,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
}

func buildContainer(conf config.K8sJobConf, spec ContainerSpec, volumeMounts []corev1.VolumeMount) corev1.Container {
	env := make([]corev1.EnvVar, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}
	for k, v := range spec.SecretEnv {
		selector := v
		env = append(env, corev1.EnvVar{
			Name: k,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &selector,
			},
		})
	}
	container := corev1.Container{
		Name:            spec.Name,
		Image:           spec.Image,
		Command:         spec.Command,
		Args:            spec.Args,
		Env:             env,
		ImagePullPolicy: corev1.PullPolicy(conf.ImagePullPolicy),
		VolumeMounts:    append([]corev1.VolumeMount(nil), volumeMounts...),
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
	return container
}
