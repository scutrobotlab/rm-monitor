package logic

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	larkbitable "github.com/larksuite/oapi-sdk-go/v3/service/bitable/v1"
	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/uploadtask"
	"scutbot.cn/web/rm-monitor/pkg/bitableupload"
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
const tableCacheTTL = 24 * 3600
const tableLockTTL = 30

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
	conf := l.svcCtx.Config.UploadConf.WithDefaults()
	if strings.TrimSpace(conf.BitableAppToken) == "" {
		return errors.New("UploadConf.BitableAppToken is required")
	}
	artifacts, err := l.svcCtx.DB.MediaArtifact.Query().
		Where(
			mediaartifact.KindEQ(mediaartifact.KindSource),
			mediaartifact.StatusEQ(mediaartifact.StatusAVAILABLE),
			mediaartifact.HasRecordTask(),
			mediaartifact.Not(mediaartifact.HasUploadTask()),
		).
		WithRecordTask(func(q *ent.RecordTaskQuery) {
			q.WithMatchRound(func(q *ent.MatchRoundQuery) {
				q.WithMatch(func(q *ent.MatchQuery) {
					q.WithRedTeam().WithBlueTeam()
				})
			})
		}).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query source artifacts")
	}
	for _, artifact := range artifacts {
		recordTask := artifact.Edges.RecordTask
		if recordTask == nil || recordTask.Edges.MatchRound == nil || recordTask.Edges.MatchRound.Edges.Match == nil {
			continue
		}
		match := recordTask.Edges.MatchRound.Edges.Match
		tableID, err := l.ensureTable(conf.BitableAppToken, bitableupload.TableName(match.Event, match.Zone))
		if err != nil {
			return err
		}
		recordID, recordURL, err := l.createBitableRecord(conf.BitableAppToken, tableID, artifact.ID, match, recordTask.Role)
		if err != nil {
			return err
		}
		if err := l.svcCtx.DB.UploadTask.Create().
			SetRecordTaskID(recordTask.ID).
			SetSourceArtifactID(artifact.ID).
			SetSourcePath(artifact.Path).
			SetBitableAppToken(conf.BitableAppToken).
			SetBitableTableID(tableID).
			SetBitableRecordID(recordID).
			SetNillableBitableRecordURL(recordURL).
			SetStatus(uploadtask.StatusPENDING).
			OnConflictColumns(uploadtask.SourceArtifactColumn).
			DoNothing().
			Exec(l.ctx); err != nil {
			return errors.Wrap(err, "create upload task")
		}
	}
	return nil
}

func (l *DispatchLogic) ensureTable(appToken, tableName string) (string, error) {
	cacheKey := fmt.Sprintf("rm-monitor:bitable:table:%s:%s", appToken, tableName)
	if tableID, err := l.svcCtx.Redis.GetCtx(l.ctx, cacheKey); err != nil {
		return "", errors.Wrap(err, "get bitable table cache")
	} else if tableID != "" {
		return tableID, nil
	}
	if tableID, err := l.findTable(appToken, tableName); err != nil {
		return "", err
	} else if tableID != "" {
		_ = l.svcCtx.Redis.SetexCtx(l.ctx, cacheKey, tableID, tableCacheTTL)
		return tableID, nil
	}
	lockKey := cacheKey + ":lock"
	locked, err := l.svcCtx.Redis.SetNXCtx(l.ctx, lockKey, "1", tableLockTTL)
	if err != nil {
		return "", errors.Wrap(err, "lock bitable table creation")
	}
	if !locked {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			if tableID, err := l.svcCtx.Redis.GetCtx(l.ctx, cacheKey); err != nil {
				return "", errors.Wrap(err, "get bitable table cache")
			} else if tableID != "" {
				return tableID, nil
			}
			if tableID, err := l.findTable(appToken, tableName); err != nil {
				return "", err
			} else if tableID != "" {
				_ = l.svcCtx.Redis.SetexCtx(l.ctx, cacheKey, tableID, tableCacheTTL)
				return tableID, nil
			}
		}
		return "", errors.Errorf("wait bitable table creation timeout: %s", tableName)
	}
	defer func() { _ = l.svcCtx.Redis.DelCtx(context.Background(), lockKey) }()
	if tableID, err := l.findTable(appToken, tableName); err != nil {
		return "", err
	} else if tableID != "" {
		_ = l.svcCtx.Redis.SetexCtx(l.ctx, cacheKey, tableID, tableCacheTTL)
		return tableID, nil
	}
	resp, err := l.svcCtx.Lark.Bitable.V1.AppTable.Create(l.ctx, larkbitable.NewCreateAppTableReqBuilder().
		AppToken(appToken).
		Body(larkbitable.NewCreateAppTableReqBodyBuilder().
			Table(larkbitable.NewReqTableBuilder().Name(tableName).Build()).
			Build()).
		Build())
	if err != nil {
		return "", errors.Wrap(err, "create bitable table")
	}
	if !resp.Success() || resp.Data == nil || resp.Data.TableId == nil {
		return "", errors.Wrap(resp, "create bitable table")
	}
	_ = l.svcCtx.Redis.SetexCtx(l.ctx, cacheKey, *resp.Data.TableId, tableCacheTTL)
	return *resp.Data.TableId, nil
}

func (l *DispatchLogic) findTable(appToken, tableName string) (string, error) {
	pageToken := ""
	for {
		builder := larkbitable.NewListAppTableReqBuilder().
			AppToken(appToken).
			PageSize(100)
		if pageToken != "" {
			builder.PageToken(pageToken)
		}
		resp, err := l.svcCtx.Lark.Bitable.V1.AppTable.List(l.ctx, builder.Build())
		if err != nil {
			return "", errors.Wrap(err, "list bitable tables")
		}
		if !resp.Success() {
			return "", errors.Errorf("list bitable tables: code=%d msg=%s", resp.Code, resp.Msg)
		}
		if resp.Data == nil {
			return "", nil
		}
		for _, table := range resp.Data.Items {
			if table.Name != nil && table.TableId != nil && *table.Name == tableName {
				return *table.TableId, nil
			}
		}
		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			return "", nil
		}
		pageToken = *resp.Data.PageToken
	}
}

func (l *DispatchLogic) createBitableRecord(appToken, tableID string, artifactID int, match *ent.Match, role string) (string, *string, error) {
	token := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("rm-monitor:upload-task:%d", artifactID))).String()
	resp, err := l.svcCtx.Lark.Bitable.V1.AppTableRecord.Create(l.ctx, larkbitable.NewCreateAppTableRecordReqBuilder().
		AppToken(appToken).
		TableId(tableID).
		ClientToken(token).
		AppTableRecord(larkbitable.NewAppTableRecordBuilder().
			Fields(bitableupload.RecordFields(match, role)).
			Build()).
		Build())
	if err != nil {
		return "", nil, errors.Wrap(err, "create bitable record")
	}
	if !resp.Success() || resp.Data == nil || resp.Data.Record == nil || resp.Data.Record.RecordId == nil {
		return "", nil, errors.Wrap(resp, "create bitable record")
	}
	record := resp.Data.Record
	if record.RecordUrl != nil && *record.RecordUrl != "" {
		return *record.RecordId, record.RecordUrl, nil
	}
	if record.SharedUrl != nil && *record.SharedUrl != "" {
		return *record.RecordId, record.SharedUrl, nil
	}
	url := fmt.Sprintf("https://scutrobotlab.feishu.cn/base/%s?table=%s&record=%s", appToken, tableID, *record.RecordId)
	return *record.RecordId, &url, nil
}

func (l *DispatchLogic) recoverDispatching() error {
	if l.svcCtx.K8s == nil {
		return nil
	}
	tasks, err := l.svcCtx.DB.UploadTask.Query().
		Where(uploadtask.StatusEQ(uploadtask.StatusDISPATCHING), uploadtask.UpdatedAtLTE(time.Now().Add(-dispatchingStaleAfter))).
		Limit(100).
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
				Args:     []string{"-f", "/etc/rm-monitor/config.yml", "-task", strconv.Itoa(task.ID)},
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
