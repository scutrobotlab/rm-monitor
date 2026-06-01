package argowf

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var workflowGVR = schema.GroupVersionResource{
	Group:    "argoproj.io",
	Version:  "v1alpha1",
	Resource: "workflows",
}

type Client struct {
	dynamic dynamic.Interface
}

func NewForDynamic(dynamicClient dynamic.Interface) *Client {
	return &Client{dynamic: dynamicClient}
}

type WorkflowTemplateRef struct {
	Namespace    string
	Name         string
	TemplateName string
	Arguments    map[string]string
	Labels       map[string]string
}

func NewForConfig(config *rest.Config) (*Client, error) {
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, errors.Wrap(err, "create argo dynamic client")
	}
	return &Client{dynamic: dyn}, nil
}

func NewInClusterOrKubeconfig(kubeconfig string) (*Client, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil && kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return nil, errors.Wrap(err, "load kubernetes config")
	}
	return NewForConfig(cfg)
}

func NewKubernetesForConfig(config *rest.Config) (*kubernetes.Clientset, error) {
	return kubernetes.NewForConfig(config)
}

func (c *Client) WorkflowExists(ctx context.Context, namespace, name string) (bool, error) {
	_, err := c.GetWorkflow(ctx, namespace, name)
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, err
}

func (c *Client) GetWorkflow(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	wf, err := c.dynamic.Resource(workflowGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrapf(err, "get workflow %s/%s", namespace, name)
	}
	return wf, nil
}

func (c *Client) ListWorkflows(ctx context.Context, namespace string, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	wfs, err := c.dynamic.Resource(workflowGVR).Namespace(namespace).List(ctx, opts)
	if err != nil {
		return nil, errors.Wrapf(err, "list workflows %s", namespace)
	}
	return wfs, nil
}

func (c *Client) EnsureWorkflowFromTemplate(ctx context.Context, ref WorkflowTemplateRef) (*unstructured.Unstructured, error) {
	if ref.Namespace == "" {
		return nil, errors.New("workflow namespace is required")
	}
	if ref.Name == "" {
		return nil, errors.New("workflow name is required")
	}
	if ref.TemplateName == "" {
		return nil, errors.New("workflow template name is required")
	}
	existing, err := c.GetWorkflow(ctx, ref.Namespace, ref.Name)
	if err == nil {
		return existing, nil
	}
	if !isNotFound(err) {
		return nil, err
	}

	args := make([]any, 0, len(ref.Arguments))
	for k, v := range ref.Arguments {
		args = append(args, map[string]any{"name": k, "value": v})
	}
	labels := map[string]any{}
	for k, v := range ref.Labels {
		labels[k] = v
	}
	wf := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Workflow",
			"metadata": map[string]any{
				"name":      ref.Name,
				"namespace": ref.Namespace,
				"labels":    labels,
			},
			"spec": map[string]any{
				"workflowTemplateRef": map[string]any{"name": ref.TemplateName},
				"arguments":           map[string]any{"parameters": args},
			},
		},
	}
	created, err := c.dynamic.Resource(workflowGVR).Namespace(ref.Namespace).Create(ctx, wf, metav1.CreateOptions{})
	if err != nil {
		if isAlreadyExists(err) {
			return c.GetWorkflow(ctx, ref.Namespace, ref.Name)
		}
		return nil, errors.Wrapf(err, "create workflow %s/%s", ref.Namespace, ref.Name)
	}
	return created, nil
}

func (c *Client) ResumeWorkflowNode(ctx context.Context, namespace, workflowName, nodeFieldSelector string) error {
	if nodeFieldSelector == "" {
		return errors.New("node field selector is required")
	}
	payload := map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Workflow",
		"metadata": map[string]any{
			"name":      workflowName,
			"namespace": namespace,
		},
		"resume": map[string]any{
			"nodeFieldSelector": nodeFieldSelector,
		},
	}
	_, err := c.dynamic.Resource(workflowGVR).Namespace(namespace).Patch(ctx, workflowName, types.MergePatchType, mustJSON(payload), metav1.PatchOptions{}, "resume")
	if err != nil {
		return errors.Wrapf(err, "resume workflow %s/%s node %s", namespace, workflowName, nodeFieldSelector)
	}
	return nil
}

func (c *Client) TerminateWorkflow(ctx context.Context, namespace, workflowName string) error {
	payload := map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Workflow",
		"metadata": map[string]any{
			"name":      workflowName,
			"namespace": namespace,
		},
		"terminate": map[string]any{},
	}
	_, err := c.dynamic.Resource(workflowGVR).Namespace(namespace).Patch(ctx, workflowName, types.MergePatchType, mustJSON(payload), metav1.PatchOptions{}, "terminate")
	if err != nil {
		return errors.Wrapf(err, "terminate workflow %s/%s", namespace, workflowName)
	}
	return nil
}

func WorkflowPhase(wf *unstructured.Unstructured) string {
	phase, _, _ := unstructured.NestedString(wf.Object, "status", "phase")
	return phase
}

func MatchWorkflowName(matchID string) string {
	return fmt.Sprintf("rm-match-%s", safeName(matchID))
}
