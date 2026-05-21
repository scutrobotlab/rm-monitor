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
	return l.dispatchPending()
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
