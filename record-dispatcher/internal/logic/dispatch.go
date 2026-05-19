package logic

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/match"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/kubejob"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/pathfmt"
	"scutbot.cn/web/rm-monitor/pkg/recording"
	"scutbot.cn/web/rm-monitor/pkg/sttcoord"
	"scutbot.cn/web/rm-monitor/record-dispatcher/internal/svc"
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
	return l.dispatchCompletedManifestJobs()
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
				SetPriority(m.Priority).
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
	name := jobName("danmu", r.ID)
	job := kubejob.Build(l.svcCtx.Config.DanmuJobConf, kubejob.JobSpec{
		Name:              name,
		App:               "danmu-job",
		Image:             jobConf.Image,
		Args:              []string{"-f", "/etc/rm-monitor/config.yml", "-round", strconv.Itoa(r.ID), "-chat-room", chatRoomID},
		CPU:               "50m",
		Memory:            "128Mi",
		PriorityClassName: kubejob.PriorityClassRecordCritical,
	})
	return l.svcCtx.K8s.CreateJob(l.ctx, jobConf.Namespace, job)
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
	jobConf := l.svcCtx.Config.STTJobConf.WithDefaults()
	name := jobName("stt", r.ID)
	job := kubejob.Build(l.svcCtx.Config.STTJobConf, kubejob.JobSpec{
		Name:          name,
		App:           "stt-job",
		ContainerName: "audio-recorder",
		Image:         jobConf.Image,
		CPU:           "100m",
		Memory:        "128Mi",
		Env:           map[string]string{"STT_SOURCE_URL": sourceURL},
		ExtraContainers: []kubejob.ContainerSpec{
			{
				Name:   "recognizer",
				Image:  jobConf.Image,
				Args:   []string{"-f", "/etc/rm-monitor/config.yml", "-mode", "recognizer", "-round", strconv.Itoa(r.ID)},
				CPU:    "100m",
				Memory: "128Mi",
			},
		},
		Args:              []string{"-f", "/etc/rm-monitor/config.yml", "-mode", "audio-recorder", "-round", strconv.Itoa(r.ID)},
		PriorityClassName: kubejob.PriorityClassRecordCritical,
	})
	if err := l.svcCtx.K8s.CreateJob(l.ctx, jobConf.Namespace, job); err != nil {
		return err
	}
	return sttcoord.SetPending(l.ctx, l.svcCtx.Redis, r.Edges.Match.ID, r.RoundNo)
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
		Order(recordtask.ByPriority(sql.OrderDesc()), recordtask.ByCreatedAt()).
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
				Name:              jobName,
				App:               "record-job",
				Image:             l.svcCtx.Config.K8sJobConf.Image,
				Args:              []string{"-f", "/etc/rm-monitor/config.yml", "-task", strconv.Itoa(task.ID)},
				CPU:               "500m",
				Memory:            "512Mi",
				MemLimit:          "1Gi",
				PriorityClassName: kubejob.PriorityClassRecordCritical,
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

func (l *DispatchLogic) dispatchCompletedManifestJobs() error {
	if l.svcCtx.K8s == nil || strings.TrimSpace(l.svcCtx.Config.ManifestJobConf.Image) == "" {
		return nil
	}
	const maxManifestJobs = 2
	conf := l.svcCtx.Config.ManifestJobConf.WithDefaults()
	active, err := l.svcCtx.K8s.CountUnfinishedJobs(l.ctx, conf.Namespace, "rm-monitor/job=manifest-job")
	if err != nil {
		return err
	}
	available := maxManifestJobs - active
	if available <= 0 {
		return nil
	}
	matches, err := l.svcCtx.DB.Match.Query().
		Where(match.LatestStatusEQ("DONE"), match.ReportIsNil()).
		WithRounds().
		Limit(50).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query completed matches for manifest")
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
			return errors.Wrap(err, "create manifest job")
		}
		created++
	}
	return nil
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
