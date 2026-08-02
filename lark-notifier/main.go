package main

import (
	"context"
	"flag"
	"os"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/logic"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/logx"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")
var backfillArtifacts = flag.Bool("backfill-artifacts", false, "backfill artifact readiness and exit")

const (
	startupLookback = 30 * time.Minute
	scanOverlap     = 5 * time.Second
	scanInterval    = 10 * time.Second
	syncTimeout     = 120 * time.Second
)

func init() {
	logx.MustSetup(logx.LogConf{
		ServiceName: "lark-notifier",
		Mode:        "console",
		Encoding:    "plain",
	})
}

func main() {
	flag.Parse()

	var c config.Config
	app.MustLoadConfig(*configFile, &c)

	svcCtx := svc.NewServiceContext(c)
	defer svcCtx.DB.Close()
	if *backfillArtifacts {
		result, err := logic.BackfillArtifactReadiness(context.Background(), svcCtx.DB, c.RecordConf)
		if err != nil {
			logx.Error(err)
			os.Exit(1)
		}
		logx.Infof("artifact readiness backfill settlements=%d highlights=%d card_sequences=%d", result.Settlements, result.Highlights, result.CardSequences)
		return
	}

	logx.Info("starting lark notifier")
	runAsLeader(context.Background(), svcCtx)
}

func runAsLeader(ctx context.Context, svcCtx *svc.ServiceContext) {
	config, err := rest.InClusterConfig()
	if err != nil {
		logx.Infof("kubernetes config unavailable; running without leader election: %v", err)
		runScanner(ctx, svcCtx)
		return
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		logx.Errorf("create kubernetes client for leader election: %v", err)
		return
	}
	identity, _ := os.Hostname()
	namespace := strings.TrimSpace(os.Getenv("POD_NAMESPACE"))
	if namespace == "" {
		namespace = "rm-monitor"
	}
	lock := &resourcelock.LeaseLock{
		LeaseMeta:  metav1.ObjectMeta{Name: "lark-notifier", Namespace: namespace},
		Client:     client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{Identity: identity},
	}
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		ReleaseOnCancel: true,
		Name:            "lark-notifier",
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leaderCtx context.Context) { runScanner(leaderCtx, svcCtx) },
			OnStoppedLeading: func() { logx.Info("lark notifier leadership lost") },
			OnNewLeader:      func(identity string) { logx.Infof("lark notifier leader=%s", identity) },
		},
	})
}

func runScanner(root context.Context, svcCtx *svc.ServiceContext) {
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()
	lastScan := time.Now().Add(-startupLookback)
	for {
		scanSince := lastScan.Add(-scanOverlap)
		scanStartedAt := time.Now()
		ctx, cancel := context.WithTimeout(root, syncTimeout)
		if err := logic.NewNotifyLogic(ctx, svcCtx).SyncWindow(scanSince); err != nil {
			logx.Errorf("lark notifier scan failed: %v", err)
		} else {
			lastScan = scanStartedAt
		}
		cancel()
		select {
		case <-root.Done():
			return
		case <-ticker.C:
		}
	}
}
