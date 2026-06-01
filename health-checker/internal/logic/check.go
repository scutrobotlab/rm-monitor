package logic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/pkg/argowf"
	"scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
)

const (
	ControllerHeartbeatKey = "rm-monitor:health:match-controller:last_success"
)

type CheckConfig struct {
	ArgoConf   config.ArgoConf
	K8sJobConf config.K8sJobConf
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
	for i := range workflows.Items {
		switch argowf.WorkflowPhase(&workflows.Items[i]) {
		case "Failed":
			failed++
		case "Error":
			errorred++
		}
	}
	if failed > 0 {
		addFailure("failed match workflows: %d", failed)
	}
	if errorred > 0 {
		addFailure("errored match workflows: %d", errorred)
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
