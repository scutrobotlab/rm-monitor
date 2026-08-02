package logic

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/larkmessage"
	"scutbot.cn/web/rm-monitor/pkg/argowf"
	"scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
)

const (
	ControllerHeartbeatKey = "rm-monitor:health:match-controller:last_success"
)

type CheckConfig struct {
	ArgoConf          config.ArgoConf
	K8sJobConf        config.K8sJobConf
	OCRServerConf     config.OCRServerConf
	WhisperServerURLs []string
}

func Run(ctx context.Context, client *ent.Client, redisClient *redisx.Client, conf CheckConfig) error {
	var failures []string
	addFailure := func(format string, args ...any) {
		failures = append(failures, fmt.Sprintf(format, args...))
	}

	if _, err := client.Match.Query().Limit(1).Count(ctx); err != nil {
		addFailure("postgres query failed: %v", err)
	}
	if err := redisClient.PingCtx(ctx); err != nil {
		addFailure("redis ping failed: %v", err)
	}
	if ok, err := controllerHeartbeatOK(ctx, redisClient); err != nil {
		addFailure("match-controller heartbeat check failed: %v", err)
	} else if !ok {
		addFailure("match-controller heartbeat missing or expired")
	}

	if err := checkRecordsWritable(conf.K8sJobConf.WithDefaults().RecordsMountPath); err != nil {
		addFailure("records pvc write check failed: %v", err)
	}
	checkArgoWorkflows(ctx, conf.ArgoConf.WithDefaults(), addFailure)
	checkInference(ctx, conf.OCRServerConf.WithDefaults(), conf.WhisperServerURLs, addFailure)
	checkLarkDeliveryErrors(ctx, client, addFailure)

	if len(failures) > 0 {
		for _, failure := range failures {
			logx.Error("health check failed: ", failure)
		}
		return errors.Errorf("health check failed with %d issue(s)", len(failures))
	}
	logx.Info("health check ok")
	return nil
}

func controllerHeartbeatOK(ctx context.Context, redisClient *redisx.Client) (bool, error) {
	val, err := redisClient.GetCtx(ctx, ControllerHeartbeatKey)
	if err != nil {
		return false, err
	}
	return val != "", nil
}

func checkArgoWorkflows(ctx context.Context, conf config.ArgoConf, addFailure func(string, ...any)) {
	if !conf.Enabled {
		return
	}
	client, err := argowf.NewInClusterOrKubeconfig(conf.Kubeconfig)
	if err != nil {
		addFailure("argo client init failed: %v", err)
		return
	}
	workflows, err := client.ListWorkflows(ctx, conf.Namespace, metav1.ListOptions{
		LabelSelector: "rm-monitor/workflow=match",
	})
	if err != nil {
		addFailure("argo workflow list failed: %v", err)
		return
	}
	failed := 0
	errorred := 0
	pendingTooLong := 0
	runningTooLong := 0
	now := time.Now()
	for i := range workflows.Items {
		wf := &workflows.Items[i]
		phase := argowf.WorkflowPhase(wf)
		finishedAt, _, _ := unstructured.NestedString(wf.Object, "status", "finishedAt")
		startedAt, _, _ := unstructured.NestedString(wf.Object, "status", "startedAt")
		switch phase {
		case "Failed":
			if recentTimestamp(finishedAt, now.Add(-6*time.Hour)) {
				failed++
			}
		case "Error":
			if recentTimestamp(finishedAt, now.Add(-6*time.Hour)) {
				errorred++
			}
		case "Pending":
			if wf.GetCreationTimestamp().Time.Before(now.Add(-30 * time.Minute)) {
				pendingTooLong++
			}
		case "Running":
			if !recentTimestamp(startedAt, now.Add(-6*time.Hour)) {
				runningTooLong++
			}
		}
	}
	if failed > 0 {
		addFailure("failed match workflows: %d", failed)
	}
	if errorred > 0 {
		addFailure("errored match workflows: %d", errorred)
	}
	if pendingTooLong > 0 {
		addFailure("match workflows pending over 30m: %d", pendingTooLong)
	}
	if runningTooLong > 0 {
		addFailure("match workflows running over 6h: %d", runningTooLong)
	}
}

func recentTimestamp(raw string, cutoff time.Time) bool {
	parsed, err := time.Parse(time.RFC3339, raw)
	return err == nil && !parsed.Before(cutoff)
}

func checkInference(ctx context.Context, ocr config.OCRServerConf, whisperURLs []string, addFailure func(string, ...any)) {
	client := &http.Client{Timeout: 5 * time.Second}
	if strings.TrimSpace(ocr.BaseURL) != "" {
		checkHTTPReady(ctx, client, strings.TrimRight(ocr.BaseURL, "/")+"/v2/health/ready", "OCR", addFailure)
	}
	for _, endpoint := range whisperURLs {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme == "" || u.Host == "" {
			addFailure("invalid Whisper endpoint %q", endpoint)
			continue
		}
		u.Path, u.RawQuery, u.Fragment = "/", "", ""
		checkHTTPReady(ctx, client, u.String(), "Whisper", addFailure)
	}
}

func checkHTTPReady(ctx context.Context, client *http.Client, endpoint, service string, addFailure func(string, ...any)) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		addFailure("%s readiness request failed: %v", service, err)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		addFailure("%s readiness failed: %v", service, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		addFailure("%s readiness returned %s", service, resp.Status)
	}
}

func checkLarkDeliveryErrors(ctx context.Context, client *ent.Client, addFailure func(string, ...any)) {
	count, err := client.LarkMessage.Query().Where(
		larkmessage.LastDeliveryErrorNotNil(),
		larkmessage.LastDeliveryAttemptAtLT(time.Now().Add(-15*time.Minute)),
	).Count(ctx)
	if err != nil {
		addFailure("query stale Lark delivery errors failed: %v", err)
		return
	}
	if count > 0 {
		addFailure("Lark message delivery errors older than 15m: %d", count)
	}
}

func checkRecordsWritable(baseDir string) error {
	if baseDir == "" {
		baseDir = "/records"
	}
	path := filepath.Join(baseDir, ".rm-monitor-health-check")
	content := []byte(time.Now().Format(time.RFC3339Nano))
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return err
	}
	readBack, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(readBack) != string(content) {
		return errors.New("read-back content mismatch")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}
