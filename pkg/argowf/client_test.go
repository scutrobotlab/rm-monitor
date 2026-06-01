package argowf

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestEnsureWorkflowFromTemplateCreatesAndReuses(t *testing.T) {
	scheme := runtime.NewScheme()
	client := NewForDynamic(dynamicfake.NewSimpleDynamicClient(scheme))
	ctx := context.Background()

	ref := WorkflowTemplateRef{
		Namespace:    "rm-monitor",
		Name:         "rm-match-1",
		TemplateName: "rm-match-workflow",
		Arguments:    map[string]string{"match_id": "1"},
		Labels:       map[string]string{"rm-monitor/workflow": "match"},
	}
	wf, err := client.EnsureWorkflowFromTemplate(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if wf.GetName() != ref.Name {
		t.Fatalf("workflow name = %s", wf.GetName())
	}
	got, err := client.EnsureWorkflowFromTemplate(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetName() != wf.GetName() {
		t.Fatalf("reused workflow name = %s", got.GetName())
	}
}

func TestWorkflowPhase(t *testing.T) {
	wf := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"phase": "Succeeded"},
	}}
	if WorkflowPhase(wf) != "Succeeded" {
		t.Fatalf("phase = %q", WorkflowPhase(wf))
	}
}
