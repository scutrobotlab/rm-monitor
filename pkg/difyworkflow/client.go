package difyworkflow

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/pkg/errors"
	"resty.dev/v3"
	common "scutbot.cn/web/rm-monitor/pkg/config"
)

type Client struct {
	baseURL string
	timeout time.Duration
}

type RunResult struct {
	WorkflowRunID string
	TaskID        string
	Outputs       map[string]json.RawMessage
	TotalTokens   int
	TotalSteps    int
}

func New(conf common.DifyConf) (*Client, error) {
	conf = conf.WithDefaults()
	baseURL := strings.TrimRight(strings.TrimSpace(conf.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("dify base url is required")
	}
	return &Client{
		baseURL: baseURL,
		timeout: time.Duration(conf.TimeoutSeconds) * time.Second,
	}, nil
}

func (c *Client) RunWorkflow(ctx context.Context, apiKey, user string, inputs map[string]any) (*RunResult, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("dify workflow api key is required")
	}
	if strings.TrimSpace(user) == "" {
		return nil, errors.New("dify workflow user is required")
	}
	var out workflowResponse
	resp, err := resty.New().
		SetTimeout(c.timeout).
		R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+apiKey).
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]any{
			"inputs":        inputs,
			"response_mode": "blocking",
			"user":          user,
		}).
		SetResult(&out).
		Post(c.baseURL + "/v1/workflows/run")
	if err != nil {
		return nil, err
	}
	if resp.IsError() {
		return nil, errors.Errorf("dify workflow http %d: %s", resp.StatusCode(), resp.String())
	}
	if out.Data.Status != "succeeded" {
		msg := strings.TrimSpace(out.Data.Error)
		if msg == "" {
			msg = strings.TrimSpace(out.Message)
		}
		if msg == "" {
			msg = "workflow status " + out.Data.Status
		}
		return nil, errors.New(msg)
	}
	if len(out.Data.Outputs) == 0 {
		return nil, errors.New("dify workflow returned empty outputs")
	}
	return &RunResult{
		WorkflowRunID: out.WorkflowRunID,
		TaskID:        out.TaskID,
		Outputs:       out.Data.Outputs,
		TotalTokens:   out.Data.TotalTokens,
		TotalSteps:    out.Data.TotalSteps,
	}, nil
}

func StringOutput(outputs map[string]json.RawMessage, key string) (string, error) {
	raw, ok := outputs[key]
	if !ok {
		return "", errors.Errorf("dify output %q is missing", key)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return "", errors.Errorf("dify output %q is empty", key)
		}
		return text, nil
	}
	text = strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return "", errors.Errorf("dify output %q is empty", key)
	}
	return text, nil
}

func RawOutput(outputs map[string]json.RawMessage, key string) (json.RawMessage, error) {
	raw, ok := outputs[key]
	if !ok {
		return nil, errors.Errorf("dify output %q is missing", key)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.Errorf("dify output %q is empty", key)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, errors.Errorf("dify output %q is empty", key)
		}
		if json.Valid([]byte(text)) {
			return json.RawMessage(text), nil
		}
		encoded, err := json.Marshal(text)
		if err != nil {
			return nil, err
		}
		return encoded, nil
	}
	if !json.Valid(raw) {
		return nil, errors.Errorf("dify output %q is not valid json", key)
	}
	return raw, nil
}

type workflowResponse struct {
	WorkflowRunID string `json:"workflow_run_id"`
	TaskID        string `json:"task_id"`
	Status        int    `json:"status"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Data          struct {
		ID          string                     `json:"id"`
		Status      string                     `json:"status"`
		Outputs     map[string]json.RawMessage `json:"outputs"`
		Error       string                     `json:"error"`
		TotalTokens int                        `json:"total_tokens"`
		TotalSteps  int                        `json:"total_steps"`
		CreatedAt   int64                      `json:"created_at"`
		FinishedAt  int64                      `json:"finished_at"`
	} `json:"data"`
}
