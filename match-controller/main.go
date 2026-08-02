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
	"scutbot.cn/web/rm-monitor/pkg/logx"

	"scutbot.cn/web/rm-monitor/match-controller/internal/config"
	"scutbot.cn/web/rm-monitor/match-controller/internal/logic"
	"scutbot.cn/web/rm-monitor/match-controller/internal/svc"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/logc"
)

var configFile = flag.String("f", "etc/config.yml", "the config file")
var roundGate = flag.Bool("round-gate", false, "write Argo round gate outputs and exit")
var roundGateMatchID = flag.String("match", "", "match id for -round-gate")
var roundGateRoundNo = flag.Int("round", 0, "round number for -round-gate")
var roundGatePlanGameCount = flag.Int("plan-game-count", 5, "planned game count for -round-gate")
var roundGateRoleSpecs = flag.String("role-specs", "[]", "role specs JSON for -round-gate")
var roundGateChatRoomID = flag.String("chat-room-id", "", "chat room id for -round-gate")
var publishAnalyze = flag.Bool("publish-analyze", false, "publish analyze artifact readiness and exit")
var publishAnalyzeRoundID = flag.Int("match-round-id", 0, "match round id for -publish-analyze")
var publishAnalyzeRoundJSON = flag.String("round-json", "", "round json path for -publish-analyze")
var publishAnalyzeImage = flag.String("settlement-image", "", "settlement image path for -publish-analyze")
var publishAnalyzeStatus = flag.String("settlement-status", "", "settlement status for -publish-analyze")

func init() {
	logx.MustSetup(logx.LogConf{
		ServiceName: "match-controller",
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
	if *publishAnalyze {
		if err := logic.PublishAnalyzeResult(context.Background(), svcCtx.DB, c.RecordConf, *publishAnalyzeRoundID, *publishAnalyzeRoundJSON, *publishAnalyzeImage, *publishAnalyzeStatus); err != nil {
			logx.Error(err)
			os.Exit(75)
		}
		return
	}

	if *roundGate {
		if err := logic.NewRoundGateLogic(context.Background(), svcCtx).Run(*roundGateMatchID, *roundGateRoundNo, *roundGatePlanGameCount, *roundGateRoleSpecs, *roundGateChatRoomID); err != nil {
			logx.Error(err)
			os.Exit(1)
		}
		return
	}

	logx.Info("starting match-controller")
	runAsLeader(context.Background(), svcCtx)
}

func runAsLeader(ctx context.Context, svcCtx *svc.ServiceContext) {
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		logx.Infof("kubernetes config unavailable; running without leader election: %v", err)
		runController(ctx, svcCtx)
		return
	}
	client, err := kubernetes.NewForConfig(kubeConfig)
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
		LeaseMeta:  metav1.ObjectMeta{Name: "match-controller", Namespace: namespace},
		Client:     client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{Identity: identity},
	}
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock: lock, LeaseDuration: 15 * time.Second, RenewDeadline: 10 * time.Second, RetryPeriod: 2 * time.Second,
		ReleaseOnCancel: true, Name: "match-controller",
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leaderCtx context.Context) { runController(leaderCtx, svcCtx) },
			OnStoppedLeading: func() { logx.Info("match-controller leadership lost") },
			OnNewLeader:      func(identity string) { logx.Infof("match-controller leader=%s", identity) },
		},
	})
}

func runController(root context.Context, svcCtx *svc.ServiceContext) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		ctx, cancel := context.WithTimeout(root, time.Minute)
		logc.Infof(ctx, "starting match scan and workflow reconciliation")
		if err := logic.NewMatchScanLogic(ctx, svcCtx).MatchScan(); err != nil {
			logc.Errorf(ctx, "match scan failed: %v", err)
		}
		if err := logic.NewWorkflowReconciler(ctx, svcCtx).Run(); err != nil {
			logc.Errorf(ctx, "workflow reconciliation failed: %v", err)
		}
		cancel()
		select {
		case <-root.Done():
			return
		case <-t.C:
		}
	}
}
