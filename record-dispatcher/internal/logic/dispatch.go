package logic

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
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
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/pathfmt"
	"scutbot.cn/web/rm-monitor/pkg/recording"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
	"scutbot.cn/web/rm-monitor/pkg/stttext"
	"scutbot.cn/web/rm-monitor/record-dispatcher/internal/svc"
)

type DispatchLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

const dispatchingStaleAfter = 5 * time.Minute

type TickStats struct {
	Source          string
	Cancelled       int
	Created         int
	Recovered       int
	Dispatched      int
	ResultApplied   int
	ManifestCreated int
}

func NewDispatchLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DispatchLogic {
	return &DispatchLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *DispatchLogic) Tick(source string) (TickStats, error) {
	stats := TickStats{Source: source}
	cancelled, err := l.cancelEndedRounds()
	if err != nil {
		return stats, err
	}
	stats.Cancelled = cancelled
	created, err := l.createTasksForStartedRounds()
	if err != nil {
		return stats, err
	}
	stats.Created = created
	applied, err := l.reconcileRecordResults()
	if err != nil {
		return stats, err
	}
	stats.ResultApplied = applied
	recovered, err := l.recoverDispatchingTasks()
	if err != nil {
		return stats, err
	}
	stats.Recovered = recovered
	dispatched, err := l.dispatchPendingTasks()
	if err != nil {
		return stats, err
	}
	stats.Dispatched = dispatched
	manifestCreated, err := l.dispatchCompletedManifestJobs()
	if err != nil {
		return stats, err
	}
	stats.ManifestCreated = manifestCreated
	return stats, nil
}

func (l *DispatchLogic) cancelEndedRounds() (int, error) {
	tasks, err := l.svcCtx.DB.RecordTask.Query().
		Where(recordtask.StatusIn(recordtask.StatusRUNNING, recordtask.StatusDISPATCHING)).
		WithMatchRound().
		Limit(200).
		All(l.ctx)
	if err != nil {
		return 0, errors.Wrap(err, "query running record tasks")
	}
	cancelled := 0
	for _, task := range tasks {
		if task.Edges.MatchRound != nil && task.Edges.MatchRound.Status == matchround.StatusENDED {
			name := jobName("record", task.ID)
			if task.K8sJobName != nil && *task.K8sJobName != "" {
				name = *task.K8sJobName
			}
			if l.svcCtx.K8s != nil {
				if err := l.svcCtx.K8s.DeleteJob(l.ctx, l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace, name); err != nil {
					return cancelled, err
				}
			}
			if err := l.svcCtx.DB.RecordTask.UpdateOneID(task.ID).SetStatus(recordtask.StatusCANCEL_REQUESTED).Exec(l.ctx); err != nil {
				return cancelled, errors.Wrap(err, "mark record stopping")
			}
			cancelled++
			l.Infof("requested stop record job task=%d round=%d job=%s", task.ID, task.Edges.MatchRound.ID, name)
			if l.svcCtx.K8s != nil && task.Edges.MatchRound != nil {
				_ = l.svcCtx.K8s.DeleteJob(l.ctx, l.svcCtx.Config.STTJobConf.WithDefaults().Namespace, jobName("stt", task.Edges.MatchRound.ID))
				_ = l.svcCtx.K8s.DeleteJob(l.ctx, l.svcCtx.Config.DanmuJobConf.WithDefaults().Namespace, jobName("danmu", task.Edges.MatchRound.ID))
				l.Infof("requested stop sidecar jobs round=%d stt_job=%s danmu_job=%s", task.Edges.MatchRound.ID, jobName("stt", task.Edges.MatchRound.ID), jobName("danmu", task.Edges.MatchRound.ID))
			}
		}
	}
	return cancelled, nil
}

func (l *DispatchLogic) recoverDispatchingTasks() (int, error) {
	if l.svcCtx.K8s == nil {
		return 0, nil
	}
	tasks, err := l.svcCtx.DB.RecordTask.Query().
		Where(recordtask.StatusEQ(recordtask.StatusDISPATCHING), recordtask.UpdatedAtLTE(time.Now().Add(-dispatchingStaleAfter))).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return 0, errors.Wrap(err, "query stale dispatching record tasks")
	}
	recovered := 0
	namespace := l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		name := jobName("record", task.ID)
		if task.K8sJobName != nil && *task.K8sJobName != "" {
			name = *task.K8sJobName
		}
		exists, err := l.svcCtx.K8s.JobExists(l.ctx, namespace, name)
		if err != nil {
			return recovered, err
		}
		if exists {
			if err := l.svcCtx.DB.RecordTask.UpdateOneID(task.ID).SetStatus(recordtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
				return recovered, errors.Wrap(err, "recover running record task")
			}
			recovered++
			l.Infof("recovered dispatching record task as running task=%d job=%s", task.ID, name)
			continue
		}
		if err := l.svcCtx.DB.RecordTask.UpdateOneID(task.ID).SetStatus(recordtask.StatusPENDING).Exec(l.ctx); err != nil {
			return recovered, errors.Wrap(err, "requeue stale record task")
		}
		recovered++
		l.Infof("requeued stale dispatching record task task=%d job=%s", task.ID, name)
	}
	return recovered, nil
}

func (l *DispatchLogic) createTasksForStartedRounds() (int, error) {
	rounds, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.StatusEQ(matchround.StatusSTARTED)).
		WithMatch(func(q *ent.MatchQuery) { q.WithRedTeam().WithBlueTeam() }).
		WithRecordTasks().
		Limit(100).
		All(l.ctx)
	if err != nil {
		return 0, errors.Wrap(err, "query started rounds")
	}
	conf := l.svcCtx.Config.RecordConf.WithDefaults()
	builders := make([]*ent.RecordTaskCreate, 0)
	for _, r := range rounds {
		m := r.Edges.Match
		if m == nil {
			continue
		}
		liveCtx, err := recording.LiveContextForZone(l.ctx, l.svcCtx.RestyClient, conf.LiveInfoURL, m.Zone, conf.Res)
		if err != nil {
			l.Errorf("live urls for match %s: %v", m.ID, err)
			continue
		}
		urls := liveCtx.URLs
		if err := l.dispatchDanmuJob(r, liveCtx.ChatRoomID); err != nil {
			l.Errorf("dispatch danmu job for round %d: %v", r.ID, err)
		}
		if err := l.dispatchSTTJob(conf, r, urls); err != nil {
			l.Errorf("dispatch stt job for round %d: %v", r.ID, err)
		}
		urls = filterBlacklistedRoles(urls, conf.RoleBlackList)
		existingRoles := make(map[string]struct{}, len(r.Edges.RecordTasks))
		for _, task := range r.Edges.RecordTasks {
			existingRoles[task.Role] = struct{}{}
		}
		for role, url := range urls {
			if _, ok := existingRoles[role]; ok {
				continue
			}
			output, err := l.outputPath(conf, m, r.RoundNo, role)
			if err != nil {
				return 0, err
			}
			builders = append(builders, l.svcCtx.DB.RecordTask.Create().
				SetMatchRoundID(r.ID).
				SetRole(role).
				SetSourceURL(url).
				SetOutputPath(output).
				SetPriority(m.Priority).
				SetStatus(recordtask.StatusPENDING))
		}
	}
	if len(builders) == 0 {
		return 0, nil
	}
	if err := l.svcCtx.DB.RecordTask.CreateBulk(builders...).
		OnConflictColumns(recordtask.MatchRoundColumn, recordtask.FieldRole).
		DoNothing().
		Exec(l.ctx); err != nil && !db.IsNoRows(err) {
		return 0, errors.Wrap(err, "bulk create record tasks")
	}
	return len(builders), nil
}

func (l *DispatchLogic) dispatchDanmuJob(r *ent.MatchRound, chatRoomID string) error {
	danmuConf := l.svcCtx.Config.DanmuConf.WithDefaults()
	jobConf := l.svcCtx.Config.DanmuJobConf.WithDefaults()
	if l.svcCtx.K8s == nil || !danmuConf.Enabled || strings.TrimSpace(jobConf.Image) == "" {
		return nil
	}
	if strings.TrimSpace(chatRoomID) == "" {
		l.Errorf("skip danmu job for round %d: chatRoomId not found", r.ID)
		return nil
	}
	jobCtx, err := l.danmuContext(r, chatRoomID)
	if err != nil {
		return err
	}
	rawCtx, err := json.Marshal(jobCtx)
	if err != nil {
		return errors.Wrap(err, "encode danmu job context")
	}
	name := jobName("danmu", r.ID)
	exists, err := l.svcCtx.K8s.JobExists(l.ctx, jobConf.Namespace, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	job := kubejob.Build(l.svcCtx.Config.DanmuJobConf, kubejob.JobSpec{
		Name:                    name,
		App:                     "danmu-job",
		Image:                   jobConf.Image,
		Args:                    []string{"-f", "/etc/rm-monitor/config.yml"},
		Env:                     map[string]string{jobcontract.EnvName: string(rawCtx)},
		CPU:                     "50m",
		Memory:                  "128Mi",
		PriorityClassName:       kubejob.PriorityClassRecordCritical,
		TerminationGraceSeconds: 60,
	})
	if err := l.svcCtx.K8s.CreateJob(l.ctx, jobConf.Namespace, job); err != nil {
		return err
	}
	l.Infof("dispatched danmu job round=%d job=%s chat_room_id=%s", r.ID, name, chatRoomID)
	return nil
}

func (l *DispatchLogic) dispatchSTTJob(conf common.RecordConf, r *ent.MatchRound, urls map[string]string) error {
	if l.svcCtx.K8s == nil || strings.TrimSpace(conf.STTRole) == "" || strings.TrimSpace(l.svcCtx.Config.STTJobConf.Image) == "" {
		return nil
	}
	sourceURL, ok := urls[conf.STTRole]
	if !ok || strings.TrimSpace(sourceURL) == "" {
		return nil
	}
	if r.Edges.Match == nil {
		return errors.New("stt round has no match edge")
	}
	jobCtx, err := l.sttContext(conf, r, sourceURL)
	if err != nil {
		return err
	}
	rawCtx, err := json.Marshal(jobCtx)
	if err != nil {
		return errors.Wrap(err, "encode stt job context")
	}
	jobConf := l.svcCtx.Config.STTJobConf.WithDefaults()
	name := jobName("stt", r.ID)
	exists, err := l.svcCtx.K8s.JobExists(l.ctx, jobConf.Namespace, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	job := kubejob.Build(l.svcCtx.Config.STTJobConf, kubejob.JobSpec{
		Name:          name,
		App:           "stt-job",
		ContainerName: "audio-recorder",
		Image:         jobConf.Image,
		CPU:           "100m",
		Memory:        "128Mi",
		Env:           map[string]string{jobcontract.EnvName: string(rawCtx)},
		ExtraContainers: []kubejob.ContainerSpec{
			{
				Name:   "recognizer",
				Image:  jobConf.Image,
				Args:   []string{"-f", "/etc/rm-monitor/config.yml", "-mode", "recognizer"},
				Env:    map[string]string{jobcontract.EnvName: string(rawCtx)},
				CPU:    "100m",
				Memory: "128Mi",
			},
		},
		Args:                    []string{"-f", "/etc/rm-monitor/config.yml", "-mode", "audio-recorder"},
		PriorityClassName:       kubejob.PriorityClassRecordCritical,
		TerminationGraceSeconds: 60,
	})
	if err := l.svcCtx.K8s.CreateJob(l.ctx, jobConf.Namespace, job); err != nil {
		return err
	}
	l.Infof("dispatched stt job round=%d job=%s role=%s", r.ID, name, conf.STTRole)
	return nil
}

func (l *DispatchLogic) danmuContext(r *ent.MatchRound, chatRoomID string) (jobcontract.DanmuContext, error) {
	conf := l.svcCtx.Config.RecordConf.WithDefaults()
	roundDir, err := l.roundDir(conf, r, "")
	if err != nil {
		return jobcontract.DanmuContext{}, err
	}
	return jobcontract.DanmuContext{
		Schema:       "rm-monitor/danmu-context/v1",
		MatchRoundID: r.ID,
		ChatRoomID:   chatRoomID,
		RoundDir:     roundDir,
		StartedAt:    r.StartedAt,
	}, nil
}

func (l *DispatchLogic) sttContext(conf common.RecordConf, r *ent.MatchRound, sourceURL string) (jobcontract.STTContext, error) {
	m := r.Edges.Match
	if m == nil {
		return jobcontract.STTContext{}, errors.New("stt round has no match edge")
	}
	red, err := m.Edges.RedTeamOrErr()
	if err != nil {
		return jobcontract.STTContext{}, err
	}
	blue, err := m.Edges.BlueTeamOrErr()
	if err != nil {
		return jobcontract.STTContext{}, err
	}
	roundDir, err := l.roundDir(conf, r, conf.STTRole)
	if err != nil {
		return jobcontract.STTContext{}, err
	}
	whisperServerURLs := resolveWhisperServerURLs(l.svcCtx.Config.WhisperServerUrls)
	if len(whisperServerURLs) == 0 {
		return jobcontract.STTContext{}, errors.New("WhisperServerUrls is empty")
	}
	return jobcontract.STTContext{
		Schema:            "rm-monitor/stt-context/v1",
		MatchRoundID:      r.ID,
		MatchID:           m.ID,
		RoundNo:           r.RoundNo,
		Role:              conf.STTRole,
		SourceURL:         sourceURL,
		RoundDir:          roundDir,
		AudioDir:          filepath.Join(roundDir, "audio"),
		STTPath:           filepath.Join(roundDir, "stt.jsonl"),
		SubtitleName:      fmt.Sprintf("%s.srt", conf.STTRole),
		WhisperServerURLs: whisperServerURLs,
		Prompt: stttext.BuildPrompt(stttext.PromptData{
			Event:      m.Event,
			Zone:       m.Zone,
			MatchID:    m.ID,
			MatchType:  m.MatchType,
			Order:      m.Order,
			RoundNo:    r.RoundNo,
			Role:       conf.STTRole,
			RedSchool:  red.SchoolName,
			RedName:    red.Name,
			BlueSchool: blue.SchoolName,
			BlueName:   blue.Name,
		}),
	}, nil
}

func resolveWhisperServerURLs(urlLists ...[]string) []string {
	var urls []string
	for _, list := range urlLists {
		urls = append(urls, list...)
	}
	return dedupeWhisperServerURLs(urls)
}

func dedupeWhisperServerURLs(urls []string) []string {
	seen := make(map[string]struct{}, len(urls))
	out := make([]string, 0, len(urls))
	for _, url := range urls {
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}
		if _, ok := seen[url]; ok {
			continue
		}
		seen[url] = struct{}{}
		out = append(out, url)
	}
	return out
}

func (l *DispatchLogic) roundDir(conf common.RecordConf, r *ent.MatchRound, role string) (string, error) {
	m := r.Edges.Match
	if m == nil {
		return "", errors.New("round has no match edge")
	}
	red, err := m.Edges.RedTeamOrErr()
	if err != nil {
		return "", err
	}
	blue, err := m.Edges.BlueTeamOrErr()
	if err != nil {
		return "", err
	}
	rel, err := pathfmt.RenderMatchDir(conf.MatchNameTemplate, conf.MatchDirTemplate, pathfmt.Data{
		Event:      m.Event,
		Zone:       m.Zone,
		Order:      m.Order,
		RedSchool:  red.SchoolName,
		RedName:    red.Name,
		BlueSchool: blue.SchoolName,
		BlueName:   blue.Name,
		RoundNo:    r.RoundNo,
		Role:       role,
	})
	if err != nil {
		return "", err
	}
	return storagepath.Resolve(conf.BaseDir, pathpkg.Join(rel, fmt.Sprintf("Round-%d", r.RoundNo))), nil
}

func filterBlacklistedRoles(urls map[string]string, blacklist []string) map[string]string {
	if len(blacklist) == 0 || len(urls) == 0 {
		return urls
	}
	blocked := make(map[string]struct{}, len(blacklist))
	for _, role := range blacklist {
		blocked[role] = struct{}{}
	}
	out := make(map[string]string, len(urls))
	for role, url := range urls {
		if _, ok := blocked[role]; ok {
			continue
		}
		out[role] = url
	}
	return out
}

func roleKeepsAudio(audioRoles []string, role string) bool {
	for _, item := range audioRoles {
		if strings.TrimSpace(item) == role {
			return true
		}
	}
	return false
}

func (l *DispatchLogic) outputPath(conf common.RecordConf, m *ent.Match, roundNo int, role string) (string, error) {
	red, err := m.Edges.RedTeamOrErr()
	if err != nil {
		return "", err
	}
	blue, err := m.Edges.BlueTeamOrErr()
	if err != nil {
		return "", err
	}
	return pathfmt.Render(conf.MatchNameTemplate, conf.PathTemplate, pathfmt.Data{
		Event:      m.Event,
		Zone:       m.Zone,
		Order:      m.Order,
		RedSchool:  red.SchoolName,
		RedName:    red.Name,
		BlueSchool: blue.SchoolName,
		BlueName:   blue.Name,
		RoundNo:    roundNo,
		Role:       role,
	})
}

func (l *DispatchLogic) dispatchPendingTasks() (int, error) {
	tasks, err := l.svcCtx.DB.RecordTask.Query().
		Where(recordtask.StatusEQ(recordtask.StatusPENDING)).
		WithMatchRound().
		Order(recordtask.ByPriority(sql.OrderDesc()), recordtask.ByCreatedAt()).
		Limit(20).
		All(l.ctx)
	if err != nil {
		return 0, errors.Wrap(err, "query pending record tasks")
	}
	dispatched := 0
	for _, task := range tasks {
		jobName := jobName("record", task.ID)
		claimed, err := l.svcCtx.DB.RecordTask.Update().
			Where(recordtask.ID(task.ID), recordtask.StatusEQ(recordtask.StatusPENDING)).
			SetStatus(recordtask.StatusDISPATCHING).
			AddAttempts(1).
			SetK8sJobName(jobName).
			Save(l.ctx)
		if err != nil {
			return dispatched, errors.Wrap(err, "mark record dispatching")
		}
		if claimed == 0 {
			continue
		}
		jobCtx := l.recordContext(task)
		rawCtx, err := json.Marshal(jobCtx)
		if err != nil {
			return dispatched, errors.Wrap(err, "encode record job context")
		}
		if l.svcCtx.K8s != nil {
			job := kubejob.Build(l.svcCtx.Config.K8sJobConf, kubejob.JobSpec{
				Name:                    jobName,
				App:                     "record-job",
				Image:                   l.svcCtx.Config.K8sJobConf.Image,
				Args:                    []string{"-f", "/etc/rm-monitor/config.yml"},
				Env:                     map[string]string{jobcontract.EnvName: string(rawCtx)},
				CPU:                     "500m",
				Memory:                  "512Mi",
				MemLimit:                "1Gi",
				PriorityClassName:       kubejob.PriorityClassRecordCritical,
				TerminationGraceSeconds: 60,
			})
			if err := l.svcCtx.K8s.CreateJob(l.ctx, l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace, job); err != nil {
				_ = l.svcCtx.DB.RecordTask.UpdateOneID(task.ID).SetStatus(recordtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(l.ctx)
				return dispatched, err
			}
		}
		if err := l.svcCtx.DB.RecordTask.UpdateOneID(task.ID).SetStatus(recordtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
			return dispatched, errors.Wrap(err, "mark record running")
		}
		dispatched++
		roundID := 0
		if task.Edges.MatchRound != nil {
			roundID = task.Edges.MatchRound.ID
		}
		l.Infof("dispatched record job task=%d round=%d role=%s job=%s output=%s", task.ID, roundID, task.Role, jobName, task.OutputPath)
		_ = db.Notify(l.ctx, l.svcCtx.Config.PostgresConf.DSN, db.RecordTaskChangedChannel, strconv.Itoa(task.ID))
	}
	return dispatched, nil
}

func (l *DispatchLogic) recordContext(task *ent.RecordTask) jobcontract.RecordContext {
	conf := l.svcCtx.Config.RecordConf.WithDefaults()
	roundID := 0
	if task.Edges.MatchRound != nil {
		roundID = task.Edges.MatchRound.ID
	}
	return jobcontract.RecordContext{
		Schema:       "rm-monitor/record-context/v1",
		RecordTaskID: task.ID,
		MatchRoundID: roundID,
		Role:         task.Role,
		SourceURL:    task.SourceURL,
		OutputPath:   task.OutputPath,
		BaseDir:      conf.BaseDir,
		KeepAudio:    roleKeepsAudio(conf.AudioRoles, task.Role),
	}
}

func (l *DispatchLogic) reconcileRecordResults() (int, error) {
	tasks, err := l.svcCtx.DB.RecordTask.Query().
		Where(recordtask.StatusIn(recordtask.StatusRUNNING, recordtask.StatusCANCEL_REQUESTED)).
		Limit(200).
		All(l.ctx)
	if err != nil {
		return 0, errors.Wrap(err, "query running record tasks for results")
	}
	applied := 0
	namespace := l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		resultPath, errorPath := l.recordResultPaths(task)
		var result jobcontract.RecordResult
		if ok, err := jobcontract.ReadJSON(resultPath, &result); err != nil {
			return applied, err
		} else if ok {
			if err := l.applyRecordResult(result); err != nil {
				return applied, err
			}
			applied++
			continue
		}
		var jobErr jobcontract.ErrorResult
		if ok, err := jobcontract.ReadJSON(errorPath, &jobErr); err != nil {
			return applied, err
		} else if ok {
			if err := l.svcCtx.DB.RecordTask.UpdateOneID(task.ID).SetStatus(recordtask.StatusFAILED).SetErrorMessage(jobErr.ErrorMessage).SetCompletedAt(time.Now()).Exec(l.ctx); err != nil {
				return applied, errors.Wrap(err, "mark record failed")
			}
			applied++
			continue
		}
		if l.svcCtx.K8s == nil || task.K8sJobName == nil || *task.K8sJobName == "" {
			continue
		}
		status, err := l.svcCtx.K8s.JobStatus(l.ctx, namespace, *task.K8sJobName)
		if err != nil {
			return applied, err
		}
		if status.State == kubejob.JobStateMissing && task.UpdatedAt.After(time.Now().Add(-2*time.Minute)) {
			continue
		}
		if (status.State == kubejob.JobStateFailed || status.State == kubejob.JobStateSucceeded) && !status.FinishedAt.IsZero() && time.Since(status.FinishedAt) < 2*time.Minute {
			continue
		}
		if status.State == kubejob.JobStateFailed || status.State == kubejob.JobStateSucceeded || status.State == kubejob.JobStateMissing {
			msg := fmt.Sprintf("record job %s finished as %s without result.json or error.json", *task.K8sJobName, status.State)
			if err := l.svcCtx.DB.RecordTask.UpdateOneID(task.ID).SetStatus(recordtask.StatusFAILED).SetErrorMessage(msg).SetCompletedAt(time.Now()).Exec(l.ctx); err != nil {
				return applied, errors.Wrap(err, "mark record missing result failed")
			}
			applied++
			l.Errorf("record job missing result task=%d job=%s state=%s", task.ID, *task.K8sJobName, status.State)
		}
	}
	return applied, nil
}

func (l *DispatchLogic) recordResultPaths(task *ent.RecordTask) (string, string) {
	conf := l.svcCtx.Config.RecordConf.WithDefaults()
	dir := filepath.Join(conf.BaseDir, filepath.FromSlash(pathpkg.Dir(filepath.ToSlash(task.OutputPath))), jobcontract.DirName, jobName("record", task.ID))
	return filepath.Join(dir, jobcontract.ResultFile), filepath.Join(dir, jobcontract.ErrorFile)
}

func (l *DispatchLogic) applyRecordResult(result jobcontract.RecordResult) error {
	completedAt := result.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	if err := l.svcCtx.DB.RecordTask.UpdateOneID(result.RecordTaskID).
		SetStatus(recordtask.StatusSUCCEEDED).
		SetCompletedAt(completedAt).
		SetFileSize(result.FileSize).
		SetChecksum(result.Checksum).
		ClearErrorMessage().
		Exec(l.ctx); err != nil {
		return errors.Wrap(err, "mark record succeeded")
	}
	if err := l.svcCtx.DB.MediaArtifact.Create().
		SetRecordTaskID(result.RecordTaskID).
		SetKind(mediaartifact.KindSource).
		SetPath(result.OutputPath).
		SetFormat(mediaartifact.FormatFlv).
		SetCodec(mediaartifact.CodecCopy).
		SetFileSize(result.FileSize).
		SetChecksum(result.Checksum).
		SetStatus(mediaartifact.StatusAVAILABLE).
		OnConflictColumns(mediaartifact.RecordTaskColumn, mediaartifact.FieldKind).
		UpdateNewValues().
		Exec(l.ctx); err != nil {
		return errors.Wrap(err, "upsert source artifact")
	}
	l.Infof("applied record result task=%d output=%s size=%d checksum=%s", result.RecordTaskID, result.OutputPath, result.FileSize, result.Checksum)
	return db.Notify(l.ctx, l.svcCtx.Config.PostgresConf.DSN, db.RecordTaskChangedChannel, strconv.Itoa(result.RecordTaskID))
}

func (l *DispatchLogic) dispatchCompletedManifestJobs() (int, error) {
	if l.svcCtx.K8s == nil || strings.TrimSpace(l.svcCtx.Config.ManifestJobConf.Image) == "" {
		return 0, nil
	}
	const maxManifestJobs = 2
	conf := l.svcCtx.Config.ManifestJobConf.WithDefaults()
	active, err := l.svcCtx.K8s.CountUnfinishedJobs(l.ctx, conf.Namespace, "rm-monitor/job=manifest-job")
	if err != nil {
		return 0, err
	}
	available := maxManifestJobs - active
	if available <= 0 {
		return 0, nil
	}
	matches, err := l.svcCtx.DB.Match.Query().
		Where(match.LatestStatusEQ("DONE"), match.ReportIsNil()).
		WithRounds().
		Order(match.ByUpdatedAt(sql.OrderDesc())).
		Limit(50).
		All(l.ctx)
	if err != nil {
		return 0, errors.Wrap(err, "query completed matches for manifest")
	}
	created := 0
	for _, m := range matches {
		if created >= available {
			break
		}
		if !completedMatch(m) {
			continue
		}
		name := manifestJobName(m.ID)
		job := kubejob.Build(l.svcCtx.Config.ManifestJobConf, kubejob.JobSpec{
			Name:   name,
			App:    "manifest-job",
			Image:  conf.Image,
			Args:   []string{"-f", "/etc/rm-monitor/config.yml", "-match", m.ID},
			CPU:    "50m",
			Memory: "128Mi",
		})
		if err := l.svcCtx.K8s.CreateJob(l.ctx, conf.Namespace, job); err != nil {
			return created, errors.Wrap(err, "create manifest job")
		}
		l.Infof("dispatched manifest job match=%s job=%s", m.ID, name)
		created++
	}
	return created, nil
}

func completedMatch(m *ent.Match) bool {
	if m == nil || len(m.Edges.Rounds) == 0 {
		return false
	}
	for _, r := range m.Edges.Rounds {
		if r.Status != matchround.StatusENDED {
			return false
		}
	}
	return true
}

func jobName(prefix string, id int) string {
	return strings.ToLower(fmt.Sprintf("%s-%d", prefix, id))
}

func manifestJobName(matchID string) string {
	h := sha1.Sum([]byte(matchID))
	return "manifest-" + hex.EncodeToString(h[:])[:16]
}
