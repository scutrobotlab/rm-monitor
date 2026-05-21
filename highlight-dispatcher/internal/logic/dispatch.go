package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/highlightclip"
	"scutbot.cn/web/rm-monitor/ent/highlightpublishtask"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
	"scutbot.cn/web/rm-monitor/highlight-dispatcher/internal/svc"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/highlight"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
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
	conf := l.svcCtx.Config.HighlightConf.WithDefaults()
	if !conf.Enabled {
		return nil
	}
	if err := l.createHighlightClips(conf); err != nil {
		return err
	}
	if err := l.recoverDispatching(); err != nil {
		return err
	}
	if err := l.recoverLostRunning(); err != nil {
		return err
	}
	if err := l.dispatchPending(); err != nil {
		return err
	}
	if err := l.createPublishTasks(); err != nil {
		return err
	}
	if err := l.reconcilePublishResults(); err != nil {
		return err
	}
	if err := l.recoverPublishDispatching(); err != nil {
		return err
	}
	if err := l.recoverLostPublishRunning(); err != nil {
		return err
	}
	return l.dispatchPendingPublish()
}

func (l *DispatchLogic) createHighlightClips(conf common.HighlightConf) error {
	recordConf := l.svcCtx.Config.RecordConf.WithDefaults()
	artifacts, err := l.svcCtx.DB.MediaArtifact.Query().
		Where(
			mediaartifact.KindEQ(mediaartifact.KindSource),
			mediaartifact.StatusEQ(mediaartifact.StatusAVAILABLE),
			mediaartifact.HasRecordTaskWith(
				recordtask.RoleEQ(conf.Role),
				recordtask.StatusEQ(recordtask.StatusSUCCEEDED),
				recordtask.HasMatchRoundWith(matchround.StatusEQ(matchround.StatusENDED)),
			),
		).
		WithRecordTask(func(q *ent.RecordTaskQuery) {
			q.WithMatchRound(func(q *ent.MatchRoundQuery) {
				q.WithMatch()
			})
		}).
		Order(mediaartifact.ByRecordTaskField(recordtask.FieldPriority, sql.OrderDesc()), mediaartifact.ByCreatedAt(sql.OrderDesc())).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query highlight source artifacts")
	}
	for _, artifact := range artifacts {
		task := artifact.Edges.RecordTask
		if task == nil || task.Edges.MatchRound == nil {
			continue
		}
		round := task.Edges.MatchRound
		roundDir := pathpkg.Dir(filepath.ToSlash(artifact.Path))
		if !fileExists(storagepath.Resolve(recordConf.BaseDir, pathpkg.Join(roundDir, "stats", "danmu-count.json"))) {
			continue
		}
		if !hasSuccessfulSTT(storagepath.Resolve(recordConf.BaseDir, pathpkg.Join(roundDir, "stt.jsonl"))) {
			continue
		}
		danmuStats, err := highlight.LoadDanmuStats(storagepath.Resolve(recordConf.BaseDir, pathpkg.Join(roundDir, "stats", "danmu-count.json")))
		if err != nil {
			l.Errorf("load danmu stats for round %d: %v", round.ID, err)
			continue
		}
		if danmuStats.Timebase != "" && danmuStats.Timebase != "record-video" {
			l.Errorf("skip highlight for round %d: unsupported danmu stats timebase %q", round.ID, danmuStats.Timebase)
			continue
		}
		var onlineStats highlight.OnlineStats
		if p := storagepath.Resolve(recordConf.BaseDir, pathpkg.Join(roundDir, "stats", "online-count.json")); fileExists(p) {
			onlineStats, _ = highlight.LoadOnlineStats(p)
		}
		candidates := highlight.FindCandidates(danmuStats, onlineStats, conf)
		for _, c := range candidates {
			outputDir := pathpkg.Join(roundDir, "highlights", fmt.Sprintf("Highlight-%02d", c.Index))
			if err := l.svcCtx.DB.HighlightClip.Create().
				SetMatchRoundID(round.ID).
				SetSourceArtifactID(artifact.ID).
				SetHighlightIndex(c.Index).
				SetRole(conf.Role).
				SetAlgorithmVersion(conf.AlgorithmVersion).
				SetStatus(highlightclip.StatusPENDING).
				SetPriority(task.Priority).
				SetStartSeconds(c.Start).
				SetEndSeconds(c.End).
				SetPeakSeconds(c.Peak).
				SetOutputDir(outputDir).
				SetScore(c.Score).
				OnConflictColumns(highlightclip.MatchRoundColumn, highlightclip.FieldRole, highlightclip.FieldAlgorithmVersion, highlightclip.FieldHighlightIndex).
				DoNothing().
				Exec(l.ctx); err != nil {
				if db.IsNoRows(err) {
					continue
				}
				return errors.Wrap(err, "create highlight clip")
			}
		}
	}
	return nil
}

func (l *DispatchLogic) recoverDispatching() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	clips, err := l.svcCtx.DB.HighlightClip.Query().
		Where(highlightclip.StatusEQ(highlightclip.StatusDISPATCHING), highlightclip.UpdatedAtLTE(time.Now().Add(-dispatchingStaleAfter))).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query stale dispatching highlight clips")
	}
	namespace := l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace
	for _, clip := range clips {
		name := jobName("highlight", clip.ID)
		if clip.K8sJobName != nil && *clip.K8sJobName != "" {
			name = *clip.K8sJobName
		}
		exists, err := l.svcCtx.K8s.JobExists(l.ctx, namespace, name)
		if err != nil {
			return err
		}
		if exists {
			if err := l.svcCtx.DB.HighlightClip.UpdateOneID(clip.ID).SetStatus(highlightclip.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
				return errors.Wrap(err, "recover running highlight clip")
			}
			continue
		}
		if err := l.svcCtx.DB.HighlightClip.UpdateOneID(clip.ID).SetStatus(highlightclip.StatusPENDING).Exec(l.ctx); err != nil {
			return errors.Wrap(err, "requeue stale highlight clip")
		}
	}
	return nil
}

func (l *DispatchLogic) recoverLostRunning() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	clips, err := l.svcCtx.DB.HighlightClip.Query().
		Where(highlightclip.StatusEQ(highlightclip.StatusRUNNING), highlightclip.UpdatedAtLTE(time.Now().Add(-dispatchingStaleAfter))).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query stale running highlight clips")
	}
	namespace := l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace
	for _, clip := range clips {
		name := jobName("highlight", clip.ID)
		if clip.K8sJobName != nil && *clip.K8sJobName != "" {
			name = *clip.K8sJobName
		}
		exists, err := l.svcCtx.K8s.JobExists(l.ctx, namespace, name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := l.svcCtx.DB.HighlightClip.UpdateOneID(clip.ID).SetStatus(highlightclip.StatusPENDING).ClearK8sJobName().Exec(l.ctx); err != nil {
			return errors.Wrap(err, "requeue lost running highlight clip")
		}
	}
	return nil
}

func (l *DispatchLogic) dispatchPending() error {
	jobConf := l.svcCtx.Config.K8sJobConf.WithDefaults()
	limit := l.svcCtx.Config.HighlightConf.WithDefaults().MaxConcurrentJobs
	if l.svcCtx.K8s != nil {
		active, err := l.svcCtx.K8s.CountUnfinishedJobs(l.ctx, jobConf.Namespace, "rm-monitor/job=highlight-artifact-job")
		if err != nil {
			return err
		}
		remaining := limit - active
		if remaining <= 0 {
			return nil
		}
		limit = remaining
	}
	clips, err := l.svcCtx.DB.HighlightClip.Query().
		Where(highlightclip.StatusEQ(highlightclip.StatusPENDING)).
		Order(highlightclip.ByPriority(sql.OrderDesc()), highlightclip.ByCreatedAt()).
		Limit(limit).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query pending highlight clips")
	}
	for _, clip := range clips {
		jobName := jobName("highlight", clip.ID)
		claimed, err := l.svcCtx.DB.HighlightClip.Update().
			Where(highlightclip.ID(clip.ID), highlightclip.StatusEQ(highlightclip.StatusPENDING)).
			SetStatus(highlightclip.StatusDISPATCHING).
			AddAttempts(1).
			SetK8sJobName(jobName).
			Save(l.ctx)
		if err != nil {
			return errors.Wrap(err, "mark highlight dispatching")
		}
		if claimed == 0 {
			continue
		}
		if l.svcCtx.K8s != nil {
			job := kubejob.Build(l.svcCtx.Config.K8sJobConf, kubejob.JobSpec{
				Name:              jobName,
				App:               "highlight-artifact-job",
				Image:             jobConf.Image,
				Args:              []string{"-f", "/etc/rm-monitor/config.yml", "-clip", strconv.Itoa(clip.ID)},
				CPU:               "2000m",
				Memory:            "1Gi",
				CPULimit:          "4000m",
				MemLimit:          "2Gi",
				PriorityClassName: kubejob.PriorityClassBackground,
				SpreadByHostname:  true,
			})
			if err := l.svcCtx.K8s.CreateJob(l.ctx, jobConf.Namespace, job); err != nil {
				_ = l.svcCtx.DB.HighlightClip.UpdateOneID(clip.ID).SetStatus(highlightclip.StatusFAILED).SetErrorMessage(err.Error()).Exec(l.ctx)
				return err
			}
		}
		if err := l.svcCtx.DB.HighlightClip.UpdateOneID(clip.ID).SetStatus(highlightclip.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
			return errors.Wrap(err, "mark highlight running")
		}
	}
	return nil
}

func (l *DispatchLogic) createPublishTasks() error {
	publishConf := l.svcCtx.Config.PublishConf.WithDefaults()
	if !publishConf.Bilibili.Enabled {
		return nil
	}
	clips, err := l.svcCtx.DB.HighlightClip.Query().
		Where(highlightclip.StatusEQ(highlightclip.StatusSUCCEEDED)).
		Order(highlightclip.ByPriority(sql.OrderDesc()), highlightclip.ByCreatedAt()).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query succeeded highlight clips for publish")
	}
	for _, clip := range clips {
		if err := l.svcCtx.DB.HighlightPublishTask.Create().
			SetHighlightClipID(clip.ID).
			SetPlatform(highlightpublishtask.PlatformBilibili).
			SetStatus(highlightpublishtask.StatusPENDING).
			SetPriority(clip.Priority).
			OnConflictColumns(highlightpublishtask.HighlightClipColumn, highlightpublishtask.FieldPlatform).
			DoNothing().
			Exec(l.ctx); err != nil {
			if db.IsNoRows(err) {
				continue
			}
			return errors.Wrap(err, "create highlight publish task")
		}
	}
	return nil
}

func (l *DispatchLogic) recoverPublishDispatching() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	tasks, err := l.svcCtx.DB.HighlightPublishTask.Query().
		Where(highlightpublishtask.StatusEQ(highlightpublishtask.StatusDISPATCHING), highlightpublishtask.UpdatedAtLTE(time.Now().Add(-dispatchingStaleAfter))).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query stale dispatching highlight publish tasks")
	}
	namespace := l.svcCtx.Config.BilibiliJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		name := jobName("highlight-publish-bilibili", task.ID)
		if task.K8sJobName != nil && *task.K8sJobName != "" {
			name = *task.K8sJobName
		}
		exists, err := l.svcCtx.K8s.JobExists(l.ctx, namespace, name)
		if err != nil {
			return err
		}
		if exists {
			if err := l.svcCtx.DB.HighlightPublishTask.UpdateOneID(task.ID).SetStatus(highlightpublishtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
				return errors.Wrap(err, "recover running highlight publish task")
			}
			continue
		}
		if err := l.svcCtx.DB.HighlightPublishTask.UpdateOneID(task.ID).SetStatus(highlightpublishtask.StatusPENDING).Exec(l.ctx); err != nil {
			return errors.Wrap(err, "requeue stale highlight publish task")
		}
	}
	return nil
}

func (l *DispatchLogic) recoverLostPublishRunning() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	tasks, err := l.svcCtx.DB.HighlightPublishTask.Query().
		Where(highlightpublishtask.StatusEQ(highlightpublishtask.StatusRUNNING), highlightpublishtask.UpdatedAtLTE(time.Now().Add(-dispatchingStaleAfter))).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query stale running highlight publish tasks")
	}
	namespace := l.svcCtx.Config.BilibiliJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		name := jobName("highlight-publish-bilibili", task.ID)
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
		if err := l.svcCtx.DB.HighlightPublishTask.UpdateOneID(task.ID).SetStatus(highlightpublishtask.StatusPENDING).ClearK8sJobName().Exec(l.ctx); err != nil {
			return errors.Wrap(err, "requeue lost running highlight publish task")
		}
	}
	return nil
}

func (l *DispatchLogic) dispatchPendingPublish() error {
	publishConf := l.svcCtx.Config.PublishConf.WithDefaults()
	if !publishConf.Bilibili.Enabled {
		return nil
	}
	jobConf := l.svcCtx.Config.BilibiliJobConf.WithDefaults()
	limit := publishConf.Bilibili.MaxConcurrentJobs
	if l.svcCtx.K8s != nil {
		active, err := l.svcCtx.K8s.CountUnfinishedJobs(l.ctx, jobConf.Namespace, "rm-monitor/job=highlight-publish-bilibili-job")
		if err != nil {
			return err
		}
		remaining := limit - active
		if remaining <= 0 {
			return nil
		}
		limit = remaining
	}
	tasks, err := l.svcCtx.DB.HighlightPublishTask.Query().
		Where(highlightpublishtask.StatusEQ(highlightpublishtask.StatusPENDING), highlightpublishtask.PlatformEQ(highlightpublishtask.PlatformBilibili)).
		WithHighlightClip(func(q *ent.HighlightClipQuery) {
			q.WithMatchRound(func(q *ent.MatchRoundQuery) {
				q.WithMatch(func(q *ent.MatchQuery) { q.WithRedTeam().WithBlueTeam() })
			})
		}).
		Order(highlightpublishtask.ByPriority(sql.OrderDesc()), highlightpublishtask.ByCreatedAt()).
		Limit(limit).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query pending highlight publish tasks")
	}
	for _, task := range tasks {
		jobName := jobName("highlight-publish-bilibili", task.ID)
		claimed, err := l.svcCtx.DB.HighlightPublishTask.Update().
			Where(highlightpublishtask.ID(task.ID), highlightpublishtask.StatusEQ(highlightpublishtask.StatusPENDING)).
			SetStatus(highlightpublishtask.StatusDISPATCHING).
			AddAttempts(1).
			SetK8sJobName(jobName).
			Save(l.ctx)
		if err != nil {
			return errors.Wrap(err, "mark highlight publish dispatching")
		}
		if claimed == 0 {
			continue
		}
		publishCtx, err := buildPublishContext(task)
		if err != nil {
			_ = l.svcCtx.DB.HighlightPublishTask.UpdateOneID(task.ID).SetStatus(highlightpublishtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(l.ctx)
			continue
		}
		publishCtxRaw, err := json.Marshal(publishCtx)
		if err != nil {
			_ = l.svcCtx.DB.HighlightPublishTask.UpdateOneID(task.ID).SetStatus(highlightpublishtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(l.ctx)
			continue
		}
		if l.svcCtx.K8s != nil {
			secretMounts := []kubejob.SecretMountSpec{}
			if strings.TrimSpace(publishConf.Bilibili.CookieSecretName) != "" {
				secretMounts = append(secretMounts, kubejob.SecretMountSpec{
					Name:       "biliup-cookie",
					SecretName: publishConf.Bilibili.CookieSecretName,
					MountPath:  "/etc/biliup",
					ReadOnly:   true,
				})
			}
			job := kubejob.Build(l.svcCtx.Config.BilibiliJobConf, kubejob.JobSpec{
				Name:              jobName,
				App:               "highlight-publish-bilibili-job",
				Image:             jobConf.Image,
				Args:              []string{"-f", "/etc/rm-monitor/config.yml"},
				Env:               map[string]string{"RM_MONITOR_PUBLISH_CONTEXT": string(publishCtxRaw)},
				CPU:               "2000m",
				Memory:            "1Gi",
				CPULimit:          "4000m",
				MemLimit:          "3Gi",
				PriorityClassName: kubejob.PriorityClassBackground,
				SpreadByHostname:  true,
				SecretMounts:      secretMounts,
			})
			if err := l.svcCtx.K8s.CreateJob(l.ctx, jobConf.Namespace, job); err != nil {
				_ = l.svcCtx.DB.HighlightPublishTask.UpdateOneID(task.ID).SetStatus(highlightpublishtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(l.ctx)
				return err
			}
		}
		if err := l.svcCtx.DB.HighlightPublishTask.UpdateOneID(task.ID).SetStatus(highlightpublishtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
			return errors.Wrap(err, "mark highlight publish running")
		}
	}
	return nil
}

type publishJobContext struct {
	TaskID         int      `json:"task_id"`
	HighlightIndex int      `json:"highlight_index"`
	StartSeconds   float64  `json:"start_seconds"`
	PeakSeconds    float64  `json:"peak_seconds"`
	OutputDir      string   `json:"output_dir"`
	LLMTitle       string   `json:"llm_title"`
	Description    string   `json:"description"`
	Tags           []string `json:"tags"`
	Event          string   `json:"event"`
	Zone           string   `json:"zone"`
	Order          int      `json:"order"`
	MatchType      string   `json:"match_type"`
	RoundNo        int      `json:"round_no"`
	RedSchool      string   `json:"red_school"`
	RedName        string   `json:"red_name"`
	BlueSchool     string   `json:"blue_school"`
	BlueName       string   `json:"blue_name"`
}

func buildPublishContext(task *ent.HighlightPublishTask) (publishJobContext, error) {
	clip := task.Edges.HighlightClip
	if clip == nil || clip.Edges.MatchRound == nil || clip.Edges.MatchRound.Edges.Match == nil {
		return publishJobContext{}, errors.New("publish task missing highlight clip match context")
	}
	match := clip.Edges.MatchRound.Edges.Match
	if match.Edges.RedTeam == nil || match.Edges.BlueTeam == nil {
		return publishJobContext{}, errors.New("publish task missing team context")
	}
	title := ""
	if clip.Title != nil {
		title = *clip.Title
	}
	description := ""
	if clip.Description != nil {
		description = *clip.Description
	}
	return publishJobContext{
		TaskID:         task.ID,
		HighlightIndex: clip.HighlightIndex,
		StartSeconds:   clip.StartSeconds,
		PeakSeconds:    clip.PeakSeconds,
		OutputDir:      clip.OutputDir,
		LLMTitle:       title,
		Description:    description,
		Tags:           clip.Tags,
		Event:          match.Event,
		Zone:           match.Zone,
		Order:          match.Order,
		MatchType:      match.MatchType,
		RoundNo:        clip.Edges.MatchRound.RoundNo,
		RedSchool:      match.Edges.RedTeam.SchoolName,
		RedName:        match.Edges.RedTeam.Name,
		BlueSchool:     match.Edges.BlueTeam.SchoolName,
		BlueName:       match.Edges.BlueTeam.Name,
	}, nil
}

type publishResultFile struct {
	ExternalID *string `json:"external_id"`
	URL        *string `json:"url"`
}

type publishErrorFile struct {
	ErrorMessage string `json:"error_message"`
}

func (l *DispatchLogic) reconcilePublishResults() error {
	recordConf := l.svcCtx.Config.RecordConf.WithDefaults()
	tasks, err := l.svcCtx.DB.HighlightPublishTask.Query().
		Where(highlightpublishtask.StatusEQ(highlightpublishtask.StatusRUNNING), highlightpublishtask.PlatformEQ(highlightpublishtask.PlatformBilibili)).
		WithHighlightClip().
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query running highlight publish tasks")
	}
	namespace := l.svcCtx.Config.BilibiliJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		clip := task.Edges.HighlightClip
		if clip == nil {
			continue
		}
		publishDir := storagepath.Resolve(recordConf.BaseDir, pathpkg.Join(clip.OutputDir, "publish"))
		resultPath := filepath.Join(publishDir, "publish-result.json")
		if raw, err := os.ReadFile(resultPath); err == nil {
			var result publishResultFile
			if err := json.Unmarshal(raw, &result); err != nil {
				return errors.Wrap(err, "parse publish result")
			}
			update := l.svcCtx.DB.HighlightPublishTask.UpdateOneID(task.ID).
				SetStatus(highlightpublishtask.StatusSUCCEEDED).
				SetCompletedAt(time.Now()).
				ClearErrorMessage()
			if result.URL != nil {
				update.SetPublishURL(*result.URL)
			}
			if result.ExternalID != nil {
				update.SetExternalID(*result.ExternalID)
			}
			if err := update.Exec(l.ctx); err != nil {
				return errors.Wrap(err, "mark publish succeeded")
			}
			continue
		}
		errorPath := filepath.Join(publishDir, "publish-error.json")
		if raw, err := os.ReadFile(errorPath); err == nil {
			var result publishErrorFile
			if err := json.Unmarshal(raw, &result); err != nil {
				return errors.Wrap(err, "parse publish error")
			}
			if result.ErrorMessage == "" {
				result.ErrorMessage = "publish job failed"
			}
			if err := l.svcCtx.DB.HighlightPublishTask.UpdateOneID(task.ID).SetStatus(highlightpublishtask.StatusFAILED).SetErrorMessage(result.ErrorMessage).SetCompletedAt(time.Now()).Exec(l.ctx); err != nil {
				return errors.Wrap(err, "mark publish failed")
			}
			continue
		}
		if l.svcCtx.K8s == nil || task.K8sJobName == nil || *task.K8sJobName == "" {
			continue
		}
		state, err := l.svcCtx.K8s.JobState(l.ctx, namespace, *task.K8sJobName)
		if err != nil {
			return err
		}
		if state == kubejob.JobStateFailed {
			if err := l.svcCtx.DB.HighlightPublishTask.UpdateOneID(task.ID).SetStatus(highlightpublishtask.StatusFAILED).SetErrorMessage("publish job failed without result file").SetCompletedAt(time.Now()).Exec(l.ctx); err != nil {
				return errors.Wrap(err, "mark publish job failed")
			}
		}
		if state == kubejob.JobStateSucceeded {
			if err := l.svcCtx.DB.HighlightPublishTask.UpdateOneID(task.ID).SetStatus(highlightpublishtask.StatusFAILED).SetErrorMessage("publish job completed without result file").SetCompletedAt(time.Now()).Exec(l.ctx); err != nil {
				return errors.Wrap(err, "mark publish job completed without result")
			}
		}
	}
	return nil
}

func fileExists(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && !stat.IsDir()
}

type sttStatusLine struct {
	Status string `json:"status"`
	Text   string `json:"text"`
}

func hasSuccessfulSTT(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row sttStatusLine
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		if row.Status == "SUCCEEDED" && strings.TrimSpace(row.Text) != "" {
			return true
		}
	}
	return false
}

func jobName(prefix string, id int) string {
	return strings.ToLower(fmt.Sprintf("%s-%d", prefix, id))
}
