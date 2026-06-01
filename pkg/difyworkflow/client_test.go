package difyworkflow

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	common "scutbot.cn/web/rm-monitor/pkg/config"
)

func TestRunWorkflowSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workflows/run" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"workflow_run_id":"run-1",
			"task_id":"task-1",
			"data":{
				"status":"succeeded",
				"outputs":{
					"report_markdown":"## 战报",
					"report_json":{"summary":"ok"}
				},
				"total_tokens":12,
				"total_steps":3
			}
		}`))
	}))
	defer server.Close()

	client, err := New(common.DifyConf{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.RunWorkflow(context.Background(), "key", "user", map[string]any{"payload": "{}"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := StringOutput(got.Outputs, "report_markdown")
	if err != nil {
		t.Fatal(err)
	}
	if report != "## 战报" {
		t.Fatalf("report = %q", report)
	}
	raw, err := RawOutput(got.Outputs, "report_json")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"summary":"ok"}` {
		t.Fatalf("report_json = %s", raw)
	}
}

func TestRunWorkflowHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadGateway)
	}))
	defer server.Close()
	client, err := New(common.DifyConf{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RunWorkflow(context.Background(), "key", "user", map[string]any{"payload": "{}"}); err == nil {
		t.Fatal("expected http error")
	}
}

func TestRunWorkflowRetriesTransientError(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"succeeded","outputs":{"report_markdown":"ok"}}}`))
	}))
	defer server.Close()
	client, err := New(common.DifyConf{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RunWorkflow(context.Background(), "key", "user", map[string]any{"payload": "{}"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestRunWorkflowTreatsHTTP2StreamErrorAsTransient(t *testing.T) {
	if isTransient(context.Canceled) {
		t.Fatal("context cancellation should not be classified as transient")
	}
	if !isTransient(errors.New("stream error: stream ID 1; INTERNAL_ERROR; received from peer")) {
		t.Fatal("HTTP/2 stream INTERNAL_ERROR should be transient")
	}
}

func TestRunWorkflowFailedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"failed","error":"provider failed"}}`))
	}))
	defer server.Close()
	client, err := New(common.DifyConf{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RunWorkflow(context.Background(), "key", "user", map[string]any{"payload": "{}"}); err == nil {
		t.Fatal("expected failed workflow error")
	}
}

func TestStringOutputMissing(t *testing.T) {
	if _, err := StringOutput(nil, "report_markdown"); err == nil {
		t.Fatal("expected missing output error")
	}
}
