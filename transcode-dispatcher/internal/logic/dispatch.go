package logic

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
	"scutbot.cn/web/rm-monitor/ent/transcodetask"
	"scutbot.cn/web/rm-monitor/ent/uploadtask"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
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
	if err := l.cleanupExpiredSources(); err != nil {
		return err
	}
	if err := l.recoverDispatching(); err != nil {
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
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query source artifacts")
	}
	for _, artifact := range artifacts {
		if err := l.svcCtx.DB.TranscodeTask.Create().
			SetSourceArtifactID(artifact.ID).
			SetStatus(transcodetask.StatusPENDING).
			OnConflictColumns(transcodetask.SourceArtifactColumn).
			DoNothing().
			Exec(l.ctx); err != nil {
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

func (l *DispatchLogic) dispatchPending() error {
	conf := l.svcCtx.Config.TranscodeConf.WithDefaults()
	if ok, err := inAllowedWindow(time.Now(), conf.AllowedWindow); err != nil {
		return err
	} else if !ok {
		return nil
	}
	if conf.SuspendWhenRecordingActive {
		active, err := l.recordingActive()
		if err != nil {
			return err
		}
		if active {
			return nil
		}
	}
	tasks, err := l.svcCtx.DB.TranscodeTask.Query().
		Where(transcodetask.StatusEQ(transcodetask.StatusPENDING)).
		Limit(100).
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
				Name:     jobName,
				App:      "transcode-job",
				Image:    jobConf.Image,
				Args:     []string{"-f", "/etc/rm-monitor/config.yml", "-task", strconv.Itoa(task.ID)},
				MountPVC: true,
				CPU:      conf.CPURequest,
				Memory:   conf.MemoryRequest,
				CPULimit: conf.CPULimit,
				MemLimit: conf.MemoryLimit,
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

func (l *DispatchLogic) recordingActive() (bool, error) {
	rounds, err := l.svcCtx.DB.MatchRound.Query().Where(matchround.StatusEQ(matchround.StatusSTARTED)).Count(l.ctx)
	if err != nil {
		return false, errors.Wrap(err, "count active rounds")
	}
	if rounds > 0 {
		return true, nil
	}
	records, err := l.svcCtx.DB.RecordTask.Query().Where(recordtask.StatusIn(recordtask.StatusDISPATCHING, recordtask.StatusRUNNING)).Count(l.ctx)
	if err != nil {
		return false, errors.Wrap(err, "count active record tasks")
	}
	return records > 0, nil
}

func (l *DispatchLogic) cleanupExpiredSources() error {
	conf := l.svcCtx.Config.TranscodeConf.WithDefaults()
	now := time.Now()
	artifacts, err := l.svcCtx.DB.MediaArtifact.Query().
		Where(
			mediaartifact.KindEQ(mediaartifact.KindSource),
			mediaartifact.StatusEQ(mediaartifact.StatusAVAILABLE),
			mediaartifact.DeletableAtNotNil(),
			mediaartifact.DeletableAtLTE(now),
		).
		WithUploadTask().
		WithSourceTranscodeTask(func(q *ent.TranscodeTaskQuery) {
			q.WithArchiveArtifact()
		}).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query deletable sources")
	}
	for _, artifact := range artifacts {
		upload := artifact.Edges.UploadTask
		transcode := artifact.Edges.SourceTranscodeTask
		if upload == nil || upload.Status != uploadtask.StatusSUCCEEDED || transcode == nil || transcode.Status != transcodetask.StatusSUCCEEDED || transcode.Edges.ArchiveArtifact == nil {
			continue
		}
		fullPath := storagepath.Resolve(conf.BaseDir, artifact.Path)
		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			l.Errorf("remove source artifact %s failed: %v", artifact.Path, err)
			continue
		}
		if err := l.svcCtx.DB.MediaArtifact.UpdateOneID(artifact.ID).
			SetStatus(mediaartifact.StatusDELETED).
			SetDeletedAt(now).
			Exec(l.ctx); err != nil {
			return errors.Wrap(err, "mark source deleted")
		}
	}
	return nil
}

func inAllowedWindow(now time.Time, window string) (bool, error) {
	parts := strings.Split(window, "-")
	if len(parts) != 2 {
		return false, fmt.Errorf("invalid transcode allowed window %q", window)
	}
	start, err := parseClock(parts[0])
	if err != nil {
		return false, err
	}
	end, err := parseClock(parts[1])
	if err != nil {
		return false, err
	}
	current := now.Hour()*60 + now.Minute()
	if start <= end {
		return current >= start && current < end, nil
	}
	return current >= start || current < end, nil
}

func parseClock(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, errors.Wrap(err, "parse transcode window")
	}
	return t.Hour()*60 + t.Minute(), nil
}

func jobName(prefix string, id int) string {
	return strings.ToLower(fmt.Sprintf("%s-%d", prefix, id))
}
