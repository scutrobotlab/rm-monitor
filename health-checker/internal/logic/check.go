package logic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
	"scutbot.cn/web/rm-monitor/ent/transcodetask"
	"scutbot.cn/web/rm-monitor/ent/uploadtask"
	"scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
)

const (
	MonitorHeartbeatKey = "rm-monitor:health:monitor:last_success"

	recentFailureWindow = 24 * time.Hour
	dispatchingStale    = 10 * time.Minute
	recordRunningStale  = 2 * time.Hour
	uploadRunningStale  = 1 * time.Hour
	transcodeStale      = 18 * time.Hour
)

type CheckConfig struct {
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
	if ok, err := monitorHeartbeatOK(ctx, redisClient); err != nil {
		addFailure("monitor heartbeat check failed: %v", err)
	} else if !ok {
		addFailure("monitor heartbeat missing or expired")
	}

	now := time.Now()
	if err := checkTaskTables(ctx, client, now, addFailure); err != nil {
		addFailure("task status check failed: %v", err)
	}
	if err := checkRecordsWritable(conf.K8sJobConf.WithDefaults().RecordsMountPath); err != nil {
		addFailure("records pvc write check failed: %v", err)
	}
	checkFailedRuntimeJobs(ctx, conf.K8sJobConf.WithDefaults().Namespace, addFailure)

	if len(failures) > 0 {
		for _, failure := range failures {
			logx.Error("health check failed: ", failure)
		}
		return errors.Errorf("health check failed with %d issue(s)", len(failures))
	}
	logx.Info("health check ok")
	return nil
}

func monitorHeartbeatOK(ctx context.Context, redisClient *redisx.Client) (bool, error) {
	val, err := redisClient.GetCtx(ctx, MonitorHeartbeatKey)
	if err != nil {
		return false, err
	}
	return val != "", nil
}

func checkTaskTables(ctx context.Context, client *ent.Client, now time.Time, addFailure func(string, ...any)) error {
	recentCutoff := now.Add(-recentFailureWindow)
	staleDispatchingCutoff := now.Add(-dispatchingStale)

	recordFailed, err := client.RecordTask.Query().Where(recordtask.StatusEQ(recordtask.StatusFAILED), recordtask.UpdatedAtGTE(recentCutoff)).Count(ctx)
	if err != nil {
		return err
	}
	recordStale, err := client.RecordTask.Query().Where(recordtask.StatusEQ(recordtask.StatusRUNNING), recordtask.UpdatedAtLTE(now.Add(-recordRunningStale))).Count(ctx)
	if err != nil {
		return err
	}
	recordDispatching, err := client.RecordTask.Query().Where(recordtask.StatusEQ(recordtask.StatusDISPATCHING), recordtask.UpdatedAtLTE(staleDispatchingCutoff)).Count(ctx)
	if err != nil {
		return err
	}
	if recordFailed > 0 || recordStale > 0 || recordDispatching > 0 {
		addFailure("record task abnormal: recent_failed=%d stale_running=%d stale_dispatching=%d", recordFailed, recordStale, recordDispatching)
	}

	uploadFailed, err := client.UploadTask.Query().Where(uploadtask.StatusEQ(uploadtask.StatusFAILED), uploadtask.UpdatedAtGTE(recentCutoff)).Count(ctx)
	if err != nil {
		return err
	}
	uploadStale, err := client.UploadTask.Query().Where(uploadtask.StatusEQ(uploadtask.StatusRUNNING), uploadtask.UpdatedAtLTE(now.Add(-uploadRunningStale))).Count(ctx)
	if err != nil {
		return err
	}
	uploadDispatching, err := client.UploadTask.Query().Where(uploadtask.StatusEQ(uploadtask.StatusDISPATCHING), uploadtask.UpdatedAtLTE(staleDispatchingCutoff)).Count(ctx)
	if err != nil {
		return err
	}
	if uploadFailed > 0 || uploadStale > 0 || uploadDispatching > 0 {
		addFailure("upload task abnormal: recent_failed=%d stale_running=%d stale_dispatching=%d", uploadFailed, uploadStale, uploadDispatching)
	}

	transcodeFailed, err := client.TranscodeTask.Query().Where(transcodetask.StatusEQ(transcodetask.StatusFAILED), transcodetask.UpdatedAtGTE(recentCutoff)).Count(ctx)
	if err != nil {
		return err
	}
	transcodeRunning, err := client.TranscodeTask.Query().Where(transcodetask.StatusEQ(transcodetask.StatusRUNNING), transcodetask.UpdatedAtLTE(now.Add(-transcodeStale))).Count(ctx)
	if err != nil {
		return err
	}
	transcodeDispatching, err := client.TranscodeTask.Query().Where(transcodetask.StatusEQ(transcodetask.StatusDISPATCHING), transcodetask.UpdatedAtLTE(staleDispatchingCutoff)).Count(ctx)
	if err != nil {
		return err
	}
	if transcodeFailed > 0 || transcodeRunning > 0 || transcodeDispatching > 0 {
		addFailure("transcode task abnormal: recent_failed=%d stale_running=%d stale_dispatching=%d", transcodeFailed, transcodeRunning, transcodeDispatching)
	}
	return nil
}

func checkFailedRuntimeJobs(ctx context.Context, namespace string, addFailure func(string, ...any)) {
	k8sClient, err := kubejob.NewInClusterClient()
	if err != nil {
		logx.Errorf("health check skipped k8s job status: %v", err)
		return
	}
	for _, app := range []string{"stt-job", "manifest-job"} {
		count, err := k8sClient.CountFailedJobs(ctx, namespace, "rm-monitor/job="+app)
		if err != nil {
			addFailure("%s failed job query failed: %v", app, err)
			continue
		}
		if count > 0 {
			addFailure("%s failed jobs: %d", app, count)
		}
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
