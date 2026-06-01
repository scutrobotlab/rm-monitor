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
	"scutbot.cn/web/rm-monitor/ent/analyzetask"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/predicate"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
	"scutbot.cn/web/rm-monitor/ent/transcodetask"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
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

func (l *DispatchLogic) createTranscodeTasks() error {
	preds := []predicate.MediaArtifact{
		mediaartifact.KindEQ(mediaartifact.KindSource),
		mediaartifact.StatusEQ(mediaartifact.StatusAVAILABLE),
		mediaartifact.Not(mediaartifact.HasSourceTranscodeTask()),
	}
	analyzeConf := l.svcCtx.Config.AnalyzeConf.WithDefaults()
	if analyzeConf.Enabled {
		preds = append(preds, mediaartifact.HasAnalyzeTasksWith(
			analyzetask.RoleEQ(analyzeConf.Role),
			analyzetask.StatusIn(analyzetask.StatusSUCCEEDED, analyzetask.StatusFAILED),
		))
	}
	artifacts, err := l.svcCtx.DB.MediaArtifact.Query().
		Where(preds...).
		WithRecordTask().
		Order(mediaartifact.ByRecordTaskField(recordtask.FieldPriority, sql.OrderDesc()), mediaartifact.ByCreatedAt()).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query source artifacts")
	}
	builders := make([]*ent.TranscodeTaskCreate, 0, len(artifacts))
	for _, artifact := range artifacts {
		priority := 0
		if artifact.Edges.RecordTask != nil {
			priority = artifact.Edges.RecordTask.Priority
		}
		builders = append(builders, l.svcCtx.DB.TranscodeTask.Create().
			SetSourceArtifactID(artifact.ID).
			SetPriority(priority).
			SetStatus(transcodetask.StatusPENDING))
	}
	if len(builders) == 0 {
		return nil
	}
	if err := l.svcCtx.DB.TranscodeTask.CreateBulk(builders...).
		OnConflictColumns(transcodetask.SourceArtifactColumn).
		DoNothing().
		Exec(l.ctx); err != nil && !db.IsNoRows(err) {
		return errors.Wrap(err, "bulk create transcode tasks")
	}
	return nil
}

func (l *DispatchLogic) recoverFinished() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	tasks, err := l.svcCtx.DB.TranscodeTask.Query().
		Where(transcodetask.StatusEQ(transcodetask.StatusRUNNING)).
		WithSourceArtifact(func(q *ent.MediaArtifactQuery) {
			q.WithRecordTask()
		}).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query running transcode tasks")
	}
	namespace := l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		name := jobName("transcode", task.ID)
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
		resultPath, errorPath, err := l.transcodeResultPaths(task)
		if err != nil {
			if err := l.failTask(task.ID, err.Error()); err != nil {
				return err
			}
			continue
		}
		var result jobcontract.TranscodeResult
		if ok, err := jobcontract.ReadJSON(resultPath, &result); err != nil {
			return err
		} else if ok {
			if err := l.applyTranscodeResult(task, result); err != nil {
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
		if err := l.failTask(task.ID, fmt.Sprintf("transcode job %s finished as %s but did not write result.json or error.json", name, state)); err != nil {
			return err
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
		WithSourceArtifact(func(q *ent.MediaArtifactQuery) {
			q.WithRecordTask(func(q *ent.RecordTaskQuery) { q.WithMatchRound() })
		}).
		Order(transcodetask.ByPriority(sql.OrderDesc()), transcodetask.ByCreatedAt()).
		Limit(limit).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query pending transcode tasks")
	}
	jobConf := l.svcCtx.Config.K8sJobConf.WithDefaults()
	for _, task := range tasks {
		if ready, err := l.sourceAnalyzeReady(task.Edges.SourceArtifact); err != nil {
			return err
		} else if !ready {
			continue
		}
		jobName := jobName("transcode", task.ID)
		jobCtx, err := l.transcodeContext(task)
		if err != nil {
			if err := l.failTask(task.ID, err.Error()); err != nil {
				return err
			}
			continue
		}
		rawCtx, err := json.Marshal(jobCtx)
		if err != nil {
			return errors.Wrap(err, "encode transcode job context")
		}
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
				Name:                    jobName,
				App:                     "transcode-job",
				Image:                   jobConf.Image,
				Args:                    []string{"-f", "/etc/rm-monitor/config.yml"},
				Env:                     map[string]string{jobcontract.EnvName: string(rawCtx)},
				CPU:                     conf.CPURequest,
				Memory:                  conf.MemoryRequest,
				CPULimit:                conf.CPULimit,
				MemLimit:                conf.MemoryLimit,
				PriorityClassName:       kubejob.PriorityClassBackground,
				SpreadByHostname:        true,
				PreferAvoidNodeLabelKey: "rm-monitor/record",
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

func (l *DispatchLogic) sourceAnalyzeReady(source *ent.MediaArtifact) (bool, error) {
	analyzeConf := l.svcCtx.Config.AnalyzeConf.WithDefaults()
	if !analyzeConf.Enabled {
		return true, nil
	}
	if source == nil || source.Edges.RecordTask == nil || source.Edges.RecordTask.Edges.MatchRound == nil {
		return false, nil
	}
	task, err := l.svcCtx.DB.AnalyzeTask.Query().
		Where(
			analyzetask.HasMatchRoundWith(matchround.ID(source.Edges.RecordTask.Edges.MatchRound.ID)),
			analyzetask.RoleEQ(analyzeConf.Role),
		).
		Only(l.ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, nil
		}
		return false, errors.Wrap(err, "query analyze task")
	}
	return task.Status == analyzetask.StatusSUCCEEDED || task.Status == analyzetask.StatusFAILED, nil
}

func (l *DispatchLogic) transcodeContext(task *ent.TranscodeTask) (jobcontract.TranscodeContext, error) {
	source := task.Edges.SourceArtifact
	if source == nil || source.Edges.RecordTask == nil {
		return jobcontract.TranscodeContext{}, errors.New("transcode task missing source artifact or record task")
	}
	conf := l.svcCtx.Config.TranscodeConf.WithDefaults()
	sourceRel, err := artifactRel(conf.BaseDir, source.Path)
	if err != nil {
		return jobcontract.TranscodeContext{}, err
	}
	archiveRel := strings.TrimSuffix(sourceRel, pathpkg.Ext(sourceRel)) + ".mp4"
	ctx := jobcontract.TranscodeContext{
		Schema:              "rm-monitor/transcode-context/v1",
		TaskID:              task.ID,
		SourceArtifactID:    source.ID,
		RecordTaskID:        source.Edges.RecordTask.ID,
		SourcePath:          sourceRel,
		ArchivePath:         archiveRel,
		BaseDir:             conf.BaseDir,
		SourceRetentionDays: conf.SourceRetentionDays,
		Role:                source.Edges.RecordTask.Role,
		RoundDir:            filepath.Join(conf.BaseDir, filepath.FromSlash(pathpkg.Dir(sourceRel))),
	}
	if source.Edges.RecordTask.Edges.MatchRound != nil {
		analyzeConf := l.svcCtx.Config.AnalyzeConf.WithDefaults()
		if analyzeConf.Enabled {
			analyzeTask, err := l.svcCtx.DB.AnalyzeTask.Query().
				Where(
					analyzetask.HasMatchRoundWith(matchround.ID(source.Edges.RecordTask.Edges.MatchRound.ID)),
					analyzetask.RoleEQ(analyzeConf.Role),
				).
				Only(l.ctx)
			if err != nil {
				if !ent.IsNotFound(err) {
					return jobcontract.TranscodeContext{}, errors.Wrap(err, "query analyze task")
				}
			} else if analyzeTask.Status == analyzetask.StatusSUCCEEDED && analyzeTask.EffectiveStartSeconds != nil && analyzeTask.EffectiveEndSeconds != nil && *analyzeTask.EffectiveEndSeconds > *analyzeTask.EffectiveStartSeconds {
				ctx.TrimStartSeconds = analyzeTask.EffectiveStartSeconds
				ctx.TrimEndSeconds = analyzeTask.EffectiveEndSeconds
			}
		}
	}
	return ctx, nil
}

func (l *DispatchLogic) transcodeResultPaths(task *ent.TranscodeTask) (string, string, error) {
	jobCtx, err := l.transcodeContext(task)
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(jobCtx.BaseDir, filepath.FromSlash(pathpkg.Dir(jobCtx.ArchivePath)), jobcontract.DirName, fmt.Sprintf("transcode-%d", jobCtx.TaskID))
	return filepath.Join(dir, jobcontract.ResultFile), filepath.Join(dir, jobcontract.ErrorFile), nil
}

func (l *DispatchLogic) applyTranscodeResult(task *ent.TranscodeTask, result jobcontract.TranscodeResult) error {
	source := task.Edges.SourceArtifact
	if source == nil || source.Edges.RecordTask == nil {
		return errors.New("transcode task missing source artifact or record task")
	}
	recordTaskID := result.RecordTaskID
	if recordTaskID == 0 {
		recordTaskID = source.Edges.RecordTask.ID
	}
	completedAt := result.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	if err := l.svcCtx.DB.MediaArtifact.Create().
		SetRecordTaskID(recordTaskID).
		SetKind(mediaartifact.KindArchive).
		SetPath(result.ArchivePath).
		SetFormat(mediaartifact.FormatMp4).
		SetCodec(mediaartifact.CodecAv1).
		SetFileSize(result.FileSize).
		SetChecksum(result.Checksum).
		SetStatus(mediaartifact.StatusAVAILABLE).
		OnConflictColumns(mediaartifact.RecordTaskColumn, mediaartifact.FieldKind).
		UpdateNewValues().
		Exec(l.ctx); err != nil {
		return errors.Wrap(err, "upsert archive artifact")
	}
	archive, err := l.svcCtx.DB.MediaArtifact.Query().
		Where(
			mediaartifact.HasRecordTaskWith(recordtask.ID(recordTaskID)),
			mediaartifact.KindEQ(mediaartifact.KindArchive),
		).
		Only(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query archive artifact")
	}
	retentionDays := l.svcCtx.Config.TranscodeConf.WithDefaults().SourceRetentionDays
	if err := l.svcCtx.DB.MediaArtifact.UpdateOneID(source.ID).
		SetDeletableAt(completedAt.AddDate(0, 0, retentionDays)).
		Exec(l.ctx); err != nil {
		return errors.Wrap(err, "set source retention")
	}
	if err := l.svcCtx.DB.TranscodeTask.UpdateOneID(task.ID).
		SetArchiveArtifactID(archive.ID).
		SetStatus(transcodetask.StatusSUCCEEDED).
		SetCompletedAt(completedAt).
		ClearErrorMessage().
		Exec(l.ctx); err != nil {
		return errors.Wrap(err, "mark transcode succeeded")
	}
	return db.Notify(l.ctx, l.svcCtx.Config.PostgresConf.DSN, db.TranscodeTaskChangedChannel, strconv.Itoa(task.ID))
}

func (l *DispatchLogic) failTask(taskID int, msg string) error {
	msg = jobcontract.Tail(msg, 4096)
	return errors.Wrap(l.svcCtx.DB.TranscodeTask.UpdateOneID(taskID).
		SetStatus(transcodetask.StatusFAILED).
		SetErrorMessage(msg).
		Exec(l.ctx), "mark transcode failed")
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
