package logic

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/pathfmt"
	"scutbot.cn/web/rm-monitor/pkg/recording"
	"scutbot.cn/web/rm-monitor/record-dispatcher/internal/svc"
)

type DispatchLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

const dispatchingStaleAfter = 5 * time.Minute
const manifestLookback = 30 * time.Second

func NewDispatchLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DispatchLogic {
	return &DispatchLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *DispatchLogic) Tick() error {
	if err := l.cancelEndedRounds(); err != nil {
		return err
	}
	if err := l.createTasksForStartedRounds(); err != nil {
		return err
	}
	if err := l.recoverDispatchingTasks(); err != nil {
		return err
	}
	if err := l.dispatchPendingTasks(); err != nil {
		return err
	}
	return l.dispatchRecentManifestJobs()
}

func (l *DispatchLogic) cancelEndedRounds() error {
	tasks, err := l.svcCtx.DB.RecordTask.Query().
		Where(recordtask.StatusIn(recordtask.StatusRUNNING, recordtask.StatusDISPATCHING)).
		WithMatchRound().
		Limit(200).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query running record tasks")
	}
	for _, task := range tasks {
		if task.Edges.MatchRound != nil && task.Edges.MatchRound.Status == matchround.StatusENDED {
			if err := l.svcCtx.DB.RecordTask.UpdateOneID(task.ID).SetStatus(recordtask.StatusCANCEL_REQUESTED).Exec(l.ctx); err != nil {
				return errors.Wrap(err, "request record cancel")
			}
			_ = db.Notify(l.ctx, l.svcCtx.Config.PostgresConf.DSN, db.RecordTaskChangedChannel, strconv.Itoa(task.ID))
		}
	}
	return nil
}

func (l *DispatchLogic) recoverDispatchingTasks() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	tasks, err := l.svcCtx.DB.RecordTask.Query().
		Where(recordtask.StatusEQ(recordtask.StatusDISPATCHING), recordtask.UpdatedAtLTE(time.Now().Add(-dispatchingStaleAfter))).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query stale dispatching record tasks")
	}
	namespace := l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace
	for _, task := range tasks {
		name := jobName("record", task.ID)
		if task.K8sJobName != nil && *task.K8sJobName != "" {
			name = *task.K8sJobName
		}
		exists, err := l.svcCtx.K8s.JobExists(l.ctx, namespace, name)
		if err != nil {
			return err
		}
		if exists {
			if err := l.svcCtx.DB.RecordTask.UpdateOneID(task.ID).SetStatus(recordtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
				return errors.Wrap(err, "recover running record task")
			}
			continue
		}
		if err := l.svcCtx.DB.RecordTask.UpdateOneID(task.ID).SetStatus(recordtask.StatusPENDING).Exec(l.ctx); err != nil {
			return errors.Wrap(err, "requeue stale record task")
		}
	}
	return nil
}

func (l *DispatchLogic) createTasksForStartedRounds() error {
	rounds, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.StatusEQ(matchround.StatusSTARTED)).
		WithMatch(func(q *ent.MatchQuery) { q.WithRedTeam().WithBlueTeam() }).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query started rounds")
	}
	conf := l.svcCtx.Config.RecordConf.WithDefaults()
	for _, r := range rounds {
		m := r.Edges.Match
		if m == nil {
			continue
		}
		urls, err := recording.LiveURLs(l.ctx, l.svcCtx.RestyClient, conf.LiveInfoURL, m.Zone, conf.Res)
		if err != nil {
			l.Errorf("live urls for match %s: %v", m.ID, err)
			continue
		}
		for role, url := range urls {
			output, err := l.outputPath(conf, m, r.RoundNo, role)
			if err != nil {
				return err
			}
			err = l.svcCtx.DB.RecordTask.Create().
				SetMatchRoundID(r.ID).
				SetRole(role).
				SetSourceURL(url).
				SetOutputPath(output).
				SetStatus(recordtask.StatusPENDING).
				OnConflictColumns(recordtask.MatchRoundColumn, recordtask.FieldRole).
				DoNothing().
				Exec(l.ctx)
			if err != nil {
				if db.IsNoRows(err) {
					continue
				}
				return errors.Wrap(err, "create record task")
			}
		}
	}
	return nil
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

func (l *DispatchLogic) dispatchPendingTasks() error {
	tasks, err := l.svcCtx.DB.RecordTask.Query().
		Where(recordtask.StatusEQ(recordtask.StatusPENDING)).
		Limit(20).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query pending record tasks")
	}
	for _, task := range tasks {
		jobName := jobName("record", task.ID)
		claimed, err := l.svcCtx.DB.RecordTask.Update().
			Where(recordtask.ID(task.ID), recordtask.StatusEQ(recordtask.StatusPENDING)).
			SetStatus(recordtask.StatusDISPATCHING).
			AddAttempts(1).
			SetK8sJobName(jobName).
			Save(l.ctx)
		if err != nil {
			return errors.Wrap(err, "mark record dispatching")
		}
		if claimed == 0 {
			continue
		}
		if l.svcCtx.K8s != nil {
			job := kubejob.Build(l.svcCtx.Config.K8sJobConf, kubejob.JobSpec{
				Name:     jobName,
				App:      "record-job",
				Image:    l.svcCtx.Config.K8sJobConf.Image,
				Args:     []string{"-f", "/etc/rm-monitor/config.yml", "-task", strconv.Itoa(task.ID)},
				MountPVC: true,
				CPU:      "500m",
				Memory:   "512Mi",
			})
			if err := l.svcCtx.K8s.CreateJob(l.ctx, l.svcCtx.Config.K8sJobConf.WithDefaults().Namespace, job); err != nil {
				_ = l.svcCtx.DB.RecordTask.UpdateOneID(task.ID).SetStatus(recordtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(l.ctx)
				return err
			}
		}
		if err := l.svcCtx.DB.RecordTask.UpdateOneID(task.ID).SetStatus(recordtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(l.ctx); err != nil {
			return errors.Wrap(err, "mark record running")
		}
		_ = db.Notify(l.ctx, l.svcCtx.Config.PostgresConf.DSN, db.RecordTaskChangedChannel, strconv.Itoa(task.ID))
	}
	return nil
}

func (l *DispatchLogic) dispatchRecentManifestJobs() error {
	if l.svcCtx.K8s == nil || strings.TrimSpace(l.svcCtx.Config.ManifestJobConf.Image) == "" {
		return nil
	}
	since := time.Now().Add(-manifestLookback)
	type manifestCandidate struct {
		match     *ent.Match
		updatedAt time.Time
	}
	matchesByID := map[string]manifestCandidate{}
	rounds, err := l.svcCtx.DB.MatchRound.Query().
		Where(matchround.UpdatedAtGTE(since)).
		WithMatch().
		Limit(200).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query recently changed rounds for manifest")
	}
	for _, r := range rounds {
		if r.Edges.Match != nil {
			cur := matchesByID[r.Edges.Match.ID]
			if cur.match == nil || r.UpdatedAt.After(cur.updatedAt) {
				matchesByID[r.Edges.Match.ID] = manifestCandidate{match: r.Edges.Match, updatedAt: r.UpdatedAt}
			}
		}
	}
	conf := l.svcCtx.Config.ManifestJobConf.WithDefaults()
	for _, item := range matchesByID {
		m := item.match
		name := manifestJobName(m.ID, item.updatedAt)
		job := kubejob.Build(l.svcCtx.Config.ManifestJobConf, kubejob.JobSpec{
			Name:     name,
			App:      "manifest-job",
			Image:    conf.Image,
			Args:     []string{"-f", "/etc/rm-monitor/config.yml", "-match", m.ID},
			MountPVC: true,
			CPU:      "50m",
			Memory:   "128Mi",
		})
		if err := l.svcCtx.K8s.CreateJob(l.ctx, conf.Namespace, job); err != nil {
			return errors.Wrap(err, "create manifest job")
		}
	}
	return nil
}

func jobName(prefix string, id int) string {
	return strings.ToLower(fmt.Sprintf("%s-%d", prefix, id))
}

func manifestJobName(matchID string, updatedAt time.Time) string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s:%d", matchID, updatedAt.UnixNano())))
	return "manifest-" + hex.EncodeToString(h[:])[:16]
}
