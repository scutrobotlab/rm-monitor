package argowf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
)

type ServerClient struct {
	baseURL   string
	tokenPath string
	client    *http.Client
}

func NewServerClient(baseURL, tokenPath string) *ServerClient {
	return &ServerClient{
		baseURL: strings.TrimRight(baseURL, "/"), tokenPath: tokenPath,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *ServerClient) RetryWorkflow(ctx context.Context, namespace, name string) error {
	payload, err := json.Marshal(map[string]any{
		"namespace":         namespace,
		"name":              name,
		"restartSuccessful": false,
	})
	if err != nil {
		return errors.Wrap(err, "marshal workflow retry request")
	}
	endpoint := fmt.Sprintf("%s/api/v1/workflows/%s/%s/retry", c.baseURL, url.PathEscape(namespace), url.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(payload))
	if err != nil {
		return errors.Wrap(err, "build workflow retry request")
	}
	req.Header.Set("Content-Type", "application/json")
	if token, readErr := os.ReadFile(c.tokenPath); readErr == nil && len(bytes.TrimSpace(token)) > 0 {
		req.Header.Set("Authorization", "Bearer "+string(bytes.TrimSpace(token)))
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return errors.Wrap(err, "call Argo workflow retry API")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.Errorf("Argo workflow retry returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
