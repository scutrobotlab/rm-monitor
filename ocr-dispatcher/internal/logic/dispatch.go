package logic

import (
	"context"
	"encoding/json"
	"fmt"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/ocrtask"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
	"scutbot.cn/web/rm-monitor/ocr-dispatcher/internal/svc"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/logx"
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
	conf := l.svcCtx.Config.OCRConf.WithDefaults()
	if !conf.Enabled {
		return nil
	}
	if err := l.createTasks(conf.Role); err != nil {
		return err
	}
	if err := l.recoverFinished(); err != nil {
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

func (l *DispatchLogic) createTasks(role string) error {
	artifacts, err := l.svcCtx.DB.MediaArtifact.Query().
		Where(
			mediaartifact.KindEQ(mediaartifact.KindSource),
			mediaartifact.StatusEQ(mediaartifact.StatusAVAILABLE),
			mediaartifact.HasRecordTaskWith(
				recordtask.RoleEQ(role),
				recordtask.HasMatchRoundWith(matchround.StatusEQ(matchround.StatusENDED)),
			),
			mediaartifact.Not(mediaartifact.HasOcrTasks()),
		).
		WithRecordTask(func(q *ent.RecordTaskQuery) {
			q.WithMatchRound()
		}).
		Order(mediaartifact.ByRecordTaskField(recordtask.FieldPriority, sql.OrderDesc()), mediaartifact.ByCreatedAt()).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query source artifacts for ocr")
	}
	builders := make([]*ent.OCRTaskCreate, 0, len(artifacts))
	for _, artifact := range artifacts {
		task := artifact.Edges.RecordTask
		if task == nil || task.Edges.MatchRound == nil {
			continue
		}
		builders = append(builders, l.svcCtx.DB.OCRTask.Create().
			SetMatchRoundID(task.Edges.MatchRound.ID).
			SetSourceArtifactID(artifact.ID).
			SetRole(role).
			SetPriority(task.Priority).
			SetStatus(ocrtask.StatusPENDING))
	}
	if len(builders) == 0 {
		return nil
	}
	if err := l.svcCtx.DB.OCRTask.CreateBulk(builders...).
		OnConflictColumns(ocrtask.MatchRoundColumn, ocrtask.FieldRole).
		DoNothing().
		Exec(l.ctx); err != nil && !ent.IsConstraintError(err) {
		return errors.Wrap(err, "bulk create ocr tasks")
	}
	return nil
}

func (l *DispatchLogic) recoverFinished() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	tasks, err := l.svcCtx.DB.OCRTask.Query().
		Where(ocrtask.StatusEQ(ocrtask.StatusRUNNING)).
		WithSourceArtifact(func(q *ent.MediaArtifactQuery) {
			q.WithRecordTask(func(q *ent.RecordTaskQuery) {
				q.WithMatchRound(func(q *ent.MatchRoundQuery) {
					q.WithMatch()
				})
			})
		}).
		WithMatchRound(func(q *ent.MatchRoundQuery) { q.WithMatch() }).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query running ocr tasks")
	}
	namespace := l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		name := jobName("ocr", task.ID)
		if task.K8sJobName != nil && *task.K8sJobName != "" {
			name = *task.K8sJobName
		}
		state, err := l.svcCtx.K8s.JobState(l.ctx, namespace, name)
		if err != nil {
			return err
		}
		if state == kubejob.JobStateRunning || state == kubejob.JobStateMissing {
			continue
		}
		resultPath, errorPath, err := l.resultPaths(task)
		if err != nil {
			if err := l.failTask(task.ID, err.Error()); err != nil {
				return err
			}
			continue
		}
		var result jobcontract.OCRResult
		if ok, err := jobcontract.ReadJSON(resultPath, &result); err != nil {
			return err
		} else if ok {
			if err := l.applyResult(task.ID, result); err != nil {
				return err
			}
			continue
		}
		var jobErr jobcontract.ErrorResult
		if ok, err := jobcontract.ReadJSON(errorPath, &jobErr); err != nil {
			return err
		} else if ok {
			if err := l.failTask(task.ID, jobErr.ErrorMessage); err != nil {
				return err
			}
			continue
		}
		if err := l.failTask(task.ID, fmt.Sprintf("ocr job %s finished as %s but did not write result.json or error.json", name, state)); err != nil {
			return err
		}
	}
	return nil
}

func (l *DispatchLogic) recoverDispatching() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	tasks, err := l.svcCtx.DB.OCRTask.Query().
		Where(ocrtask.StatusEQ(ocrtask.StatusDISPATCHING), ocrtask.UpdatedAtLTE(time.Now().Add(-dispatchingStaleAfter))).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query stale dispatching ocr tasks")
	}
	namespace := l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		name := jobName("ocr", task.ID)
		if task.K8sJobName != nil && *task.K8sJobName != "" {
			name = *task.K8sJobName
		}
		exists, err := l.svcCtx.K8s.JobExists(l.ctx, namespace, name)
		if err != nil {
			return err
		}
		if exists {
			if err := l.svcCtx.DB.OCRTask.UpdateOneID(task.ID).SetStatus(ocrtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
				return errors.Wrap(err, "recover running ocr task")
			}
			continue
		}
		if err := l.svcCtx.DB.OCRTask.UpdateOneID(task.ID).SetStatus(ocrtask.StatusPENDING).ClearK8sJobName().Exec(l.ctx); err != nil {
			return errors.Wrap(err, "requeue stale ocr task")
		}
	}
	return nil
}

func (l *DispatchLogic) recoverLostRunning() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	tasks, err := l.svcCtx.DB.OCRTask.Query().
		Where(ocrtask.StatusEQ(ocrtask.StatusRUNNING), ocrtask.UpdatedAtLTE(time.Now().Add(-dispatchingStaleAfter))).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query stale running ocr tasks")
	}
	namespace := l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		name := jobName("ocr", task.ID)
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
		if err := l.svcCtx.DB.OCRTask.UpdateOneID(task.ID).SetStatus(ocrtask.StatusPENDING).ClearK8sJobName().Exec(l.ctx); err != nil {
			return errors.Wrap(err, "requeue lost running ocr task")
		}
	}
	return nil
}

func (l *DispatchLogic) dispatchPending() error {
	ocrConf := l.svcCtx.Config.OCRConf.WithDefaults()
	running, err := l.svcCtx.DB.OCRTask.Query().
		Where(ocrtask.StatusIn(ocrtask.StatusDISPATCHING, ocrtask.StatusRUNNING)).
		Count(l.ctx)
	if err != nil {
		return errors.Wrap(err, "count running ocr tasks")
	}
	limit := ocrConf.MaxConcurrentJobs - running
	if limit <= 0 {
		return nil
	}
	tasks, err := l.svcCtx.DB.OCRTask.Query().
		Where(ocrtask.StatusEQ(ocrtask.StatusPENDING)).
		WithSourceArtifact(func(q *ent.MediaArtifactQuery) {
			q.WithRecordTask(func(q *ent.RecordTaskQuery) {
				q.WithMatchRound(func(q *ent.MatchRoundQuery) {
					q.WithMatch()
				})
			})
		}).
		WithMatchRound(func(q *ent.MatchRoundQuery) { q.WithMatch() }).
		Order(ocrtask.ByPriority(sql.OrderDesc()), ocrtask.ByCreatedAt()).
		Limit(limit).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query pending ocr tasks")
	}
	jobConf := l.svcCtx.Config.K8sJobConf.WithDefaults()
	for _, task := range tasks {
		name := jobName("ocr", task.ID)
		jobCtx, err := l.ocrContext(task)
		if err != nil {
			if err := l.failTask(task.ID, err.Error()); err != nil {
				return err
			}
			continue
		}
		rawCtx, err := json.Marshal(jobCtx)
		if err != nil {
			return errors.Wrap(err, "encode ocr job context")
		}
		claimed, err := l.svcCtx.DB.OCRTask.Update().
			Where(ocrtask.ID(task.ID), ocrtask.StatusEQ(ocrtask.StatusPENDING)).
			SetStatus(ocrtask.StatusDISPATCHING).
			AddAttempts(1).
			SetK8sJobName(name).
			Save(l.ctx)
		if err != nil {
			return errors.Wrap(err, "mark ocr dispatching")
		}
		if claimed == 0 {
			continue
		}
		if l.svcCtx.K8s != nil {
			job := kubejob.Build(l.svcCtx.Config.K8sJobConf, kubejob.JobSpec{
				Name:                    name,
				App:                     "ocr-job",
				Image:                   jobConf.Image,
				Env:                     map[string]string{jobcontract.EnvName: string(rawCtx)},
				CPU:                     "500m",
				Memory:                  "1Gi",
				MemLimit:                "2Gi",
				PriorityClassName:       kubejob.PriorityClassBackground,
				TerminationGraceSeconds: 30,
			})
			if err := l.svcCtx.K8s.CreateJob(l.ctx, jobConf.Namespace, job); err != nil {
				_ = l.svcCtx.DB.OCRTask.UpdateOneID(task.ID).SetStatus(ocrtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(l.ctx)
				return err
			}
		}
		if err := l.svcCtx.DB.OCRTask.UpdateOneID(task.ID).SetStatus(ocrtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
			return errors.Wrap(err, "mark ocr running")
		}
	}
	return nil
}

func (l *DispatchLogic) ocrContext(task *ent.OCRTask) (jobcontract.OCRContext, error) {
	source := task.Edges.SourceArtifact
	if source == nil || source.Edges.RecordTask == nil || source.Edges.RecordTask.Edges.MatchRound == nil {
		return jobcontract.OCRContext{}, errors.New("ocr task missing source artifact or record task round")
	}
	round := source.Edges.RecordTask.Edges.MatchRound
	matchID := ""
	if round.Edges.Match != nil {
		matchID = round.Edges.Match.ID
	}
	conf := l.svcCtx.Config.OCRConf.WithDefaults()
	baseDir := l.svcCtx.Config.K8sJobConf.WithDefaults().RecordsMountPath
	sourceRel, err := artifactRel(baseDir, source.Path)
	if err != nil {
		return jobcontract.OCRContext{}, err
	}
	return jobcontract.OCRContext{
		Schema:              "rm-monitor/ocr-context/v1",
		TaskID:              task.ID,
		MatchRoundID:        round.ID,
		SourceArtifactID:    source.ID,
		MatchID:             matchID,
		RoundNo:             round.RoundNo,
		Role:                task.Role,
		SourcePath:          sourceRel,
		RoundDir:            filepath.Join(baseDir, filepath.FromSlash(pathpkg.Dir(sourceRel))),
		BaseDir:             baseDir,
		FrameInterval:       conf.FrameInterval,
		SimilarityThreshold: conf.SimilarityThreshold,
	}, nil
}

func (l *DispatchLogic) resultPaths(task *ent.OCRTask) (string, string, error) {
	jobCtx, err := l.ocrContext(task)
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(jobCtx.RoundDir, jobcontract.DirName, fmt.Sprintf("ocr-%d", jobCtx.TaskID))
	return filepath.Join(dir, jobcontract.ResultFile), filepath.Join(dir, jobcontract.ErrorFile), nil
}

func (l *DispatchLogic) applyResult(taskID int, result jobcontract.OCRResult) error {
	completedAt := result.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	if err := l.svcCtx.DB.OCRTask.UpdateOneID(taskID).
		SetStatus(ocrtask.StatusSUCCEEDED).
		SetSettlementPath(result.SettlementPath).
		SetSettlementJSON(result.SettlementJSON).
		SetCompletedAt(completedAt).
		ClearErrorMessage().
		Exec(l.ctx); err != nil {
		return errors.Wrap(err, "mark ocr succeeded")
	}
	return db.Notify(l.ctx, l.svcCtx.Config.PostgresConf.DSN, db.RecordTaskChangedChannel, strconv.Itoa(taskID))
}

func (l *DispatchLogic) failTask(taskID int, msg string) error {
	msg = jobcontract.Tail(msg, 4096)
	return errors.Wrap(l.svcCtx.DB.OCRTask.UpdateOneID(taskID).
		SetStatus(ocrtask.StatusFAILED).
		SetErrorMessage(msg).
		SetCompletedAt(time.Now()).
		Exec(l.ctx), "mark ocr failed")
}

func artifactRel(baseDir, artifactPath string) (string, error) {
	p := pathpkg.Clean(filepath.ToSlash(strings.TrimSpace(artifactPath)))
	if p == "." || p == "/" {
		return "", errors.New("artifact path is empty")
	}
	base := pathpkg.Clean(filepath.ToSlash(baseDir))
	if base == "." || base == "/" {
		return "", errors.Errorf("invalid base dir %q", baseDir)
	}
	if strings.HasPrefix(p, "/") {
		if p == base {
			return "", errors.Errorf("artifact path %q points to base dir", artifactPath)
		}
		prefix := strings.TrimSuffix(base, "/") + "/"
		if !strings.HasPrefix(p, prefix) {
			return "", errors.Errorf("artifact path %q is outside base dir %q", artifactPath, baseDir)
		}
		p = strings.TrimPrefix(p, prefix)
	}
	if strings.HasPrefix(p, "../") || p == ".." {
		return "", errors.Errorf("artifact path %q escapes records root", artifactPath)
	}
	return p, nil
}

func jobName(prefix string, id int) string {
	return strings.ToLower(fmt.Sprintf("%s-%d", prefix, id))
}
