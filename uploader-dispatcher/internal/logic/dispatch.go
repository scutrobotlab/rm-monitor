package logic

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/uploadtask"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/uploader-dispatcher/internal/svc"
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
	if err := l.createUploadTasks(); err != nil {
		return err
	}
	if err := l.recoverDispatching(); err != nil {
		return err
	}
	return l.dispatchPending()
}

func (l *DispatchLogic) createUploadTasks() error {
	artifacts, err := l.svcCtx.DB.MediaArtifact.Query().
		Where(
			mediaartifact.KindEQ(mediaartifact.KindSource),
			mediaartifact.StatusEQ(mediaartifact.StatusAVAILABLE),
		).
		WithRecordTask().
		WithUploadTask().
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query source artifacts")
	}
	for _, artifact := range artifacts {
		if artifact.Edges.UploadTask != nil || artifact.Edges.RecordTask == nil {
			continue
		}
		if err := l.svcCtx.DB.UploadTask.Create().
			SetRecordTaskID(artifact.Edges.RecordTask.ID).
			SetSourceArtifactID(artifact.ID).
			SetSourcePath(artifact.Path).
			SetStatus(uploadtask.StatusPENDING).
			OnConflictColumns(uploadtask.SourceArtifactColumn).
			DoNothing().
			Exec(l.ctx); err != nil {
			return errors.Wrap(err, "create upload task")
		}
	}
	return nil
}

func (l *DispatchLogic) recoverDispatching() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	tasks, err := l.svcCtx.DB.UploadTask.Query().
		Where(uploadtask.StatusEQ(uploadtask.StatusDISPATCHING), uploadtask.UpdatedAtLTE(time.Now().Add(-dispatchingStaleAfter))).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query stale dispatching upload tasks")
	}
	namespace := l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		name := jobName("upload", task.ID)
		if task.K8sJobName != nil && *task.K8sJobName != "" {
			name = *task.K8sJobName
		}
		exists, err := l.svcCtx.K8s.JobExists(l.ctx, namespace, name)
		if err != nil {
			return err
		}
		if exists {
			if err := l.svcCtx.DB.UploadTask.UpdateOneID(task.ID).SetStatus(uploadtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
				return errors.Wrap(err, "recover running upload task")
			}
			continue
		}
		if err := l.svcCtx.DB.UploadTask.UpdateOneID(task.ID).SetStatus(uploadtask.StatusPENDING).Exec(l.ctx); err != nil {
			return errors.Wrap(err, "requeue stale upload task")
		}
	}
	return nil
}

func (l *DispatchLogic) dispatchPending() error {
	conf := l.svcCtx.Config.UploadConf.WithDefaults()
	running, err := l.svcCtx.DB.UploadTask.Query().Where(uploadtask.StatusIn(uploadtask.StatusDISPATCHING, uploadtask.StatusRUNNING)).Count(l.ctx)
	if err != nil {
		return errors.Wrap(err, "count running upload tasks")
	}
	limit := conf.Concurrency - running
	if limit <= 0 {
		return nil
	}
	tasks, err := l.svcCtx.DB.UploadTask.Query().Where(uploadtask.StatusEQ(uploadtask.StatusPENDING)).Limit(limit).All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query pending upload tasks")
	}
	for _, task := range tasks {
		jobName := jobName("upload", task.ID)
		claimed, err := l.svcCtx.DB.UploadTask.Update().
			Where(uploadtask.ID(task.ID), uploadtask.StatusEQ(uploadtask.StatusPENDING)).
			SetStatus(uploadtask.StatusDISPATCHING).
			AddAttempts(1).
			SetK8sJobName(jobName).
			Save(l.ctx)
		if err != nil {
			return errors.Wrap(err, "mark upload dispatching")
		}
		if claimed == 0 {
			continue
		}
		if l.svcCtx.K8s != nil {
			job := kubejob.Build(l.svcCtx.Config.K8sJobConf, kubejob.JobSpec{
				Name:     jobName,
				App:      "uploader-job",
				Image:    l.svcCtx.Config.K8sJobConf.Image,
				Args:     []string{"-f", "/app/etc/config.yml", "-task", strconv.Itoa(task.ID)},
				MountPVC: true,
				CPU:      "100m",
				Memory:   "256Mi",
			})
			if err := l.svcCtx.K8s.CreateJob(l.ctx, l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace, job); err != nil {
				_ = l.svcCtx.DB.UploadTask.UpdateOneID(task.ID).SetStatus(uploadtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(l.ctx)
				return err
			}
		}
		if err := l.svcCtx.DB.UploadTask.UpdateOneID(task.ID).SetStatus(uploadtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
			return errors.Wrap(err, "mark upload running")
		}
	}
	return nil
}

func jobName(prefix string, id int) string {
	return strings.ToLower(fmt.Sprintf("%s-%d", prefix, id))
}
