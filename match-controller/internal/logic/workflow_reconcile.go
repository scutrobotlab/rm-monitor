package logic

import (
	"context"
	"strconv"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/match-controller/internal/svc"
	"scutbot.cn/web/rm-monitor/pkg/argowf"
	"scutbot.cn/web/rm-monitor/pkg/logx"
)

const recoveryAttemptAnnotation = "rm-monitor/recovery-attempts"

type WorkflowReconciler struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewWorkflowReconciler(ctx context.Context, svcCtx *svc.ServiceContext) *WorkflowReconciler {
	return &WorkflowReconciler{ctx: ctx, svcCtx: svcCtx}
}

func (r *WorkflowReconciler) Run() error {
	conf := r.svcCtx.Config.ArgoConf.WithDefaults()
	if !conf.Enabled || r.svcCtx.ArgoClient == nil {
		return nil
	}
	workflows, err := r.svcCtx.ArgoClient.ListWorkflows(r.ctx, conf.Namespace, metav1.ListOptions{LabelSelector: "rm-monitor/workflow=match"})
	if err != nil {
		return err
	}
	var firstErr error
	for i := range workflows.Items {
		wf := &workflows.Items[i]
		matchID := wf.GetLabels()["rm-monitor/match-id"]
		if matchID == "" {
			continue
		}
		phase := argowf.WorkflowPhase(wf)
		uid := string(wf.GetUID())
		if _, err := r.svcCtx.DB.Match.Update().Where(match.IDEQ(matchID), match.Or(
			match.WorkflowNameIsNil(), match.WorkflowNameNEQ(wf.GetName()),
			match.WorkflowUIDIsNil(), match.WorkflowUIDNEQ(uid),
			match.WorkflowPhaseIsNil(), match.WorkflowPhaseNEQ(phase),
		)).SetWorkflowName(wf.GetName()).SetWorkflowUID(uid).SetWorkflowPhase(phase).Save(r.ctx); err != nil {
			firstErr = errors.Wrapf(err, "sync match %s workflow", matchID)
			continue
		}
		if _, err := r.svcCtx.DB.MatchRound.Update().Where(matchround.HasMatchWith(match.IDEQ(matchID)), matchround.Or(
			matchround.WorkflowNameIsNil(), matchround.WorkflowNameNEQ(wf.GetName()),
			matchround.WorkflowUIDIsNil(), matchround.WorkflowUIDNEQ(uid),
			matchround.WorkflowPhaseIsNil(), matchround.WorkflowPhaseNEQ(phase),
		)).SetWorkflowName(wf.GetName()).SetWorkflowUID(uid).SetWorkflowPhase(phase).Save(r.ctx); err != nil {
			firstErr = errors.Wrapf(err, "sync match %s round workflows", matchID)
			continue
		}
		if phase != "Error" || r.svcCtx.ArgoServer == nil {
			continue
		}
		attempts, _ := strconv.Atoi(wf.GetAnnotations()[recoveryAttemptAnnotation])
		if attempts >= 1 {
			continue
		}
		if err := r.svcCtx.ArgoServer.RetryWorkflow(r.ctx, conf.Namespace, wf.GetName()); err != nil {
			logx.Errorf("retry errored workflow %s/%s: %v", conf.Namespace, wf.GetName(), err)
			firstErr = err
			continue
		}
		if err := r.svcCtx.ArgoClient.UpdateWorkflowAnnotations(r.ctx, wf, map[string]string{
			recoveryAttemptAnnotation: "1",
			"rm-monitor/recovered-at": time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			logx.Errorf("mark workflow %s recovery: %v", wf.GetName(), err)
		}
		logx.Infof("retried errored workflow %s/%s with restartSuccessful=false", conf.Namespace, wf.GetName())
	}
	return firstErr
}
