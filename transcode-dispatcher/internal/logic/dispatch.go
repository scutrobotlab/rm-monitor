package logic

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
	"scutbot.cn/web/rm-monitor/ent/transcodetask"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/transcode-dispatcher/internal/svc"
)

type DispatchLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

const dispatchingStaleAfter = 5 * time.Minute

func NewDispatchLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DispatchLogic {
	return &DispatchLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *DispatchLogic) Tick() error {
	if err := l.createTranscodeTasks(); err != nil {
		return err
	}
	if err := l.recoverDispatching(); err != nil {
		return err
	}
	if err := l.recoverLostRunning(); err != nil {
		return err
	}
	return l.dispatchPending()
}

func (l *DispatchLogic) createTranscodeTasks() error {
	artifacts, err := l.svcCtx.DB.MediaArtifact.Query().
		Where(
			mediaartifact.KindEQ(mediaartifact.KindSource),
			mediaartifact.StatusEQ(mediaartifact.StatusAVAILABLE),
			mediaartifact.Not(mediaartifact.HasSourceTranscodeTask()),
		).
		WithRecordTask().
		Order(mediaartifact.ByRecordTaskField(recordtask.FieldPriority, sql.OrderDesc()), mediaartifact.ByCreatedAt()).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query source artifacts")
	}
	for _, artifact := range artifacts {
		priority := 0
		if artifact.Edges.RecordTask != nil {
			priority = artifact.Edges.RecordTask.Priority
		}
		if err := l.svcCtx.DB.TranscodeTask.Create().
			SetSourceArtifactID(artifact.ID).
			SetPriority(priority).
			SetStatus(transcodetask.StatusPENDING).
			OnConflictColumns(transcodetask.SourceArtifactColumn).
			DoNothing().
			Exec(l.ctx); err != nil {
			if db.IsNoRows(err) {
				continue
			}
			return errors.Wrap(err, "create transcode task")
		}
	}
	return nil
}

func (l *DispatchLogic) recoverDispatching() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	tasks, err := l.svcCtx.DB.TranscodeTask.Query().
		Where(transcodetask.StatusEQ(transcodetask.StatusDISPATCHING), transcodetask.UpdatedAtLTE(time.Now().Add(-dispatchingStaleAfter))).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query stale dispatching transcode tasks")
	}
	namespace := l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		name := jobName("transcode", task.ID)
		if task.K8sJobName != nil && *task.K8sJobName != "" {
			name = *task.K8sJobName
		}
		exists, err := l.svcCtx.K8s.JobExists(l.ctx, namespace, name)
		if err != nil {
			return err
		}
		if exists {
			if err := l.svcCtx.DB.TranscodeTask.UpdateOneID(task.ID).SetStatus(transcodetask.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
				return errors.Wrap(err, "recover running transcode task")
			}
			continue
		}
		if err := l.svcCtx.DB.TranscodeTask.UpdateOneID(task.ID).SetStatus(transcodetask.StatusPENDING).Exec(l.ctx); err != nil {
			return errors.Wrap(err, "requeue stale transcode task")
		}
	}
	return nil
}

func (l *DispatchLogic) recoverLostRunning() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	tasks, err := l.svcCtx.DB.TranscodeTask.Query().
		Where(transcodetask.StatusEQ(transcodetask.StatusRUNNING), transcodetask.UpdatedAtLTE(time.Now().Add(-dispatchingStaleAfter))).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query stale running transcode tasks")
	}
	namespace := l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		name := jobName("transcode", task.ID)
		if task.K8sJobName != nil && *task.K8sJobName != "" {
			name = *task.K8sJobName
		}
		exists, err := l.svcCtx.K8s.JobExists(l.ctx, namespace, name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := l.svcCtx.DB.TranscodeTask.UpdateOneID(task.ID).
			SetStatus(transcodetask.StatusPENDING).
			ClearK8sJobName().
			Exec(l.ctx); err != nil {
			return errors.Wrap(err, "requeue lost running transcode task")
		}
	}
	return nil
}

func (l *DispatchLogic) dispatchPending() error {
	conf := l.svcCtx.Config.TranscodeConf.WithDefaults()
	running, err := l.svcCtx.DB.TranscodeTask.Query().
		Where(transcodetask.StatusIn(transcodetask.StatusDISPATCHING, transcodetask.StatusRUNNING)).
		Count(l.ctx)
	if err != nil {
		return errors.Wrap(err, "count running transcode tasks")
	}
	limit := conf.MaxConcurrent - running
	if limit <= 0 {
		return nil
	}
	tasks, err := l.svcCtx.DB.TranscodeTask.Query().
		Where(transcodetask.StatusEQ(transcodetask.StatusPENDING)).
		Order(transcodetask.ByPriority(sql.OrderDesc()), transcodetask.ByCreatedAt()).
		Limit(limit).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query pending transcode tasks")
	}
	jobConf := l.svcCtx.Config.K8sJobConf.WithDefaults()
	for _, task := range tasks {
		jobName := jobName("transcode", task.ID)
		claimed, err := l.svcCtx.DB.TranscodeTask.Update().
			Where(transcodetask.ID(task.ID), transcodetask.StatusEQ(transcodetask.StatusPENDING)).
			SetStatus(transcodetask.StatusDISPATCHING).
			AddAttempts(1).
			SetK8sJobName(jobName).
			Save(l.ctx)
		if err != nil {
			return errors.Wrap(err, "mark transcode dispatching")
		}
		if claimed == 0 {
			continue
		}
		if l.svcCtx.K8s != nil {
			job := kubejob.Build(l.svcCtx.Config.K8sJobConf, kubejob.JobSpec{
				Name:              jobName,
				App:               "transcode-job",
				Image:             jobConf.Image,
				Args:              []string{"-f", "/etc/rm-monitor/config.yml", "-task", strconv.Itoa(task.ID)},
				CPU:               conf.CPURequest,
				Memory:            conf.MemoryRequest,
				CPULimit:          conf.CPULimit,
				MemLimit:          conf.MemoryLimit,
				PriorityClassName: kubejob.PriorityClassBackground,
				SpreadByHostname:  true,
			})
			if err := l.svcCtx.K8s.CreateJob(l.ctx, jobConf.Namespace, job); err != nil {
				_ = l.svcCtx.DB.TranscodeTask.UpdateOneID(task.ID).SetStatus(transcodetask.StatusFAILED).SetErrorMessage(err.Error()).Exec(l.ctx)
				return err
			}
		}
		if err := l.svcCtx.DB.TranscodeTask.UpdateOneID(task.ID).SetStatus(transcodetask.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
			return errors.Wrap(err, "mark transcode running")
		}
	}
	return nil
}

func jobName(prefix string, id int) string {
	return strings.ToLower(fmt.Sprintf("%s-%d", prefix, id))
}
