package logic

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	larkbitable "github.com/larksuite/oapi-sdk-go/v3/service/bitable/v1"
	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/mediaartifact"
	"scutbot.cn/web/rm-monitor/ent/recordtask"
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
const uploadTaskCreateLockTTL = 120
const uploadTaskPrepareLockTTL = 120

func NewDispatchLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DispatchLogic {
	return &DispatchLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *DispatchLogic) Tick() error {
	if err := l.createUploadTasks(); err != nil {
		return err
	}
	if err := l.prepareUploadTasks(); err != nil {
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
		Order(mediaartifact.ByRecordTaskField(recordtask.FieldPriority, sql.OrderDesc()), mediaartifact.ByCreatedAt()).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query source artifacts")
	}
	for _, artifact := range artifacts {
		created, err := l.createUploadTaskForArtifact(conf.BitableAppToken, artifact)
		if err != nil {
			return err
		}
		if !created {
			continue
		}
	}
	return nil
}

func (l *DispatchLogic) createUploadTaskForArtifact(appToken string, artifact *ent.MediaArtifact) (bool, error) {
	lockKey := fmt.Sprintf("rm-monitor:upload-task:create:%d", artifact.ID)
	locked, err := l.svcCtx.Redis.SetNXCtx(l.ctx, lockKey, "1", uploadTaskCreateLockTTL)
	if err != nil {
		return false, errors.Wrap(err, "lock upload task creation")
	}
	if !locked {
		return false, nil
	}
	defer func() { _ = l.svcCtx.Redis.DelCtx(context.Background(), lockKey) }()

	exists, err := l.svcCtx.DB.MediaArtifact.Query().
		Where(mediaartifact.ID(artifact.ID), mediaartifact.HasUploadTask()).
		Exist(l.ctx)
	if err != nil {
		return false, errors.Wrap(err, "check existing upload task")
	}
	if exists {
		return false, nil
	}

	recordTask := artifact.Edges.RecordTask
	if recordTask == nil || recordTask.Edges.MatchRound == nil || recordTask.Edges.MatchRound.Edges.Match == nil {
		return false, nil
	}
	if _, err := l.svcCtx.DB.UploadTask.Create().
		SetRecordTaskID(recordTask.ID).
		SetSourceArtifactID(artifact.ID).
		SetSourcePath(artifact.Path).
		SetPriority(recordTask.Priority).
		SetStatus(uploadtask.StatusPENDING).
		Save(l.ctx); err != nil {
		if ent.IsConstraintError(err) {
			return false, nil
		}
		return false, errors.Wrap(err, "create upload task placeholder")
	}
	return true, nil
}

func (l *DispatchLogic) prepareUploadTasks() error {
	conf := l.svcCtx.Config.UploadConf.WithDefaults()
	tasks, err := l.svcCtx.DB.UploadTask.Query().
		Where(
			uploadtask.StatusEQ(uploadtask.StatusPENDING),
			uploadtask.BitableRecordIDIsNil(),
		).
		WithSourceArtifact().
		WithRecordTask(func(q *ent.RecordTaskQuery) {
			q.WithMatchRound(func(q *ent.MatchRoundQuery) {
				q.WithMatch(func(q *ent.MatchQuery) {
					q.WithRedTeam().WithBlueTeam()
				})
			})
		}).
		Order(uploadtask.ByPriority(sql.OrderDesc()), uploadtask.ByCreatedAt()).
		Limit(100).
		All(l.ctx)
	if err != nil {
		return errors.Wrap(err, "query upload tasks missing bitable context")
	}
	for _, task := range tasks {
		if err := l.prepareUploadTask(conf.BitableAppToken, task); err != nil {
			return err
		}
	}
	return nil
}

func (l *DispatchLogic) prepareUploadTask(appToken string, task *ent.UploadTask) error {
	if task.Edges.SourceArtifact == nil || task.Edges.RecordTask == nil || task.Edges.RecordTask.Edges.MatchRound == nil || task.Edges.RecordTask.Edges.MatchRound.Edges.Match == nil {
		return nil
	}
	lockKey := fmt.Sprintf("rm-monitor:upload-task:prepare:%d", task.ID)
	locked, err := l.svcCtx.Redis.SetNXCtx(l.ctx, lockKey, "1", uploadTaskPrepareLockTTL)
	if err != nil {
		return errors.Wrap(err, "lock upload task preparation")
	}
	if !locked {
		return nil
	}
	defer func() { _ = l.svcCtx.Redis.DelCtx(context.Background(), lockKey) }()

	needsPrepare, err := l.svcCtx.DB.UploadTask.Query().
		Where(uploadtask.ID(task.ID), uploadtask.BitableRecordIDIsNil()).
		Exist(l.ctx)
	if err != nil {
		return errors.Wrap(err, "check upload task preparation")
	}
	if !needsPrepare {
		return nil
	}
	recordTask := task.Edges.RecordTask
	match := recordTask.Edges.MatchRound.Edges.Match
	tableID, err := l.ensureTable(appToken, bitableupload.TableName(match.Event, match.Zone))
	if err != nil {
		return err
	}
	recordID, recordURL, err := l.createBitableRecord(appToken, tableID, task.Edges.SourceArtifact, match, recordTask.Edges.MatchRound.RoundNo, recordTask.Role)
	if err != nil {
		return err
	}
	updated, err := l.svcCtx.DB.UploadTask.Update().
		Where(uploadtask.ID(task.ID), uploadtask.BitableRecordIDIsNil()).
		SetBitableAppToken(appToken).
		SetBitableTableID(tableID).
		SetBitableRecordID(recordID).
		SetNillableBitableRecordURL(recordURL).
		Save(l.ctx)
	if err != nil {
		return errors.Wrap(err, "update upload task bitable context")
	}
	if updated == 0 {
		return nil
	}
	return nil
}

func (l *DispatchLogic) ensureTable(appToken, tableName string) (string, error) {
	cacheKey := fmt.Sprintf("rm-monitor:bitable:table:%s:%s", appToken, tableName)
	if tableID, err := l.svcCtx.Redis.GetCtx(l.ctx, cacheKey); err != nil {
		return "", errors.Wrap(err, "get bitable table cache")
	} else if tableID != "" {
		if err := l.ensureTableFields(appToken, tableID); err != nil {
			return "", err
		}
		return tableID, nil
	}
	if tableID, err := l.findTable(appToken, tableName); err != nil {
		return "", err
	} else if tableID != "" {
		if err := l.ensureTableFields(appToken, tableID); err != nil {
			return "", err
		}
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
				if err := l.ensureTableFields(appToken, tableID); err != nil {
					return "", err
				}
				return tableID, nil
			}
			if tableID, err := l.findTable(appToken, tableName); err != nil {
				return "", err
			} else if tableID != "" {
				if err := l.ensureTableFields(appToken, tableID); err != nil {
					return "", err
				}
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
		if err := l.ensureTableFields(appToken, tableID); err != nil {
			return "", err
		}
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
	if err := l.ensureTableFields(appToken, *resp.Data.TableId); err != nil {
		return "", err
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

func (l *DispatchLogic) ensureTableFields(appToken, tableID string) error {
	lockKey := fmt.Sprintf("rm-monitor:bitable:fields:%s:%s:lock", appToken, tableID)
	locked, err := l.svcCtx.Redis.SetNXCtx(l.ctx, lockKey, "1", tableLockTTL)
	if err != nil {
		return errors.Wrap(err, "lock bitable field creation")
	}
	if !locked {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			existing, err := l.listFields(appToken, tableID)
			if err != nil {
				return err
			}
			if requiredFieldsReady(existing) {
				return nil
			}
		}
		return errors.Errorf("wait bitable field creation timeout: %s", tableID)
	}
	defer func() { _ = l.svcCtx.Redis.DelCtx(context.Background(), lockKey) }()
	return l.ensureTableFieldsLocked(appToken, tableID)
}

func (l *DispatchLogic) ensureTableFieldsLocked(appToken, tableID string) error {
	existing, err := l.listFields(appToken, tableID)
	if err != nil {
		return err
	}
	for name, fieldType := range requiredBitableFields() {
		if existingField, ok := existing[name]; ok {
			if existingField.Type == fieldType {
				continue
			}
			if err := l.updateFieldType(appToken, tableID, existingField.ID, name, fieldType); err != nil {
				return err
			}
			continue
		}
		resp, err := l.svcCtx.Lark.Bitable.V1.AppTableField.Create(l.ctx, larkbitable.NewCreateAppTableFieldReqBuilder().
			AppToken(appToken).
			TableId(tableID).
			AppTableField(larkbitable.NewAppTableFieldBuilder().
				FieldName(name).
				Type(fieldType).
				Build()).
			Build())
		if err != nil {
			return errors.Wrapf(err, "create bitable field %s", name)
		}
		if !resp.Success() {
			return errors.Errorf("create bitable field %s: code=%d msg=%s", name, resp.Code, resp.Msg)
		}
	}
	return nil
}

func requiredFieldsReady(existing map[string]bitableFieldInfo) bool {
	for name, fieldType := range requiredBitableFields() {
		existingField, ok := existing[name]
		if !ok || existingField.Type != fieldType {
			return false
		}
	}
	return true
}

func requiredBitableFields() map[string]int {
	return map[string]int{
		bitableupload.FieldRole:       larkbitable.TypeSingleSelect,
		bitableupload.FieldMatch:      larkbitable.TypeNumber,
		bitableupload.FieldRound:      larkbitable.TypeNumber,
		bitableupload.FieldType:       larkbitable.TypeSingleSelect,
		bitableupload.FieldRedTeam:    larkbitable.TypeSingleSelect,
		bitableupload.FieldBlueTeam:   larkbitable.TypeSingleSelect,
		bitableupload.FieldAttachment: larkbitable.TypeAttachment,
	}
}

type bitableFieldInfo struct {
	ID   string
	Type int
}

func (l *DispatchLogic) listFields(appToken, tableID string) (map[string]bitableFieldInfo, error) {
	fields := make(map[string]bitableFieldInfo)
	pageToken := ""
	for {
		builder := larkbitable.NewListAppTableFieldReqBuilder().
			AppToken(appToken).
			TableId(tableID).
			PageSize(100)
		if pageToken != "" {
			builder.PageToken(pageToken)
		}
		resp, err := l.svcCtx.Lark.Bitable.V1.AppTableField.List(l.ctx, builder.Build())
		if err != nil {
			return nil, errors.Wrap(err, "list bitable fields")
		}
		if !resp.Success() {
			return nil, errors.Errorf("list bitable fields: code=%d msg=%s", resp.Code, resp.Msg)
		}
		if resp.Data == nil {
			return fields, nil
		}
		for _, field := range resp.Data.Items {
			if field.FieldName == nil || field.FieldId == nil || field.Type == nil {
				continue
			}
			fields[*field.FieldName] = bitableFieldInfo{ID: *field.FieldId, Type: *field.Type}
		}
		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			return fields, nil
		}
		pageToken = *resp.Data.PageToken
	}
}

func (l *DispatchLogic) updateFieldType(appToken, tableID, fieldID, fieldName string, fieldType int) error {
	resp, err := l.svcCtx.Lark.Bitable.V1.AppTableField.Update(l.ctx, larkbitable.NewUpdateAppTableFieldReqBuilder().
		AppToken(appToken).
		TableId(tableID).
		FieldId(fieldID).
		AppTableField(larkbitable.NewAppTableFieldBuilder().
			FieldName(fieldName).
			Type(fieldType).
			Build()).
		Build())
	if err != nil {
		return errors.Wrapf(err, "update bitable field %s", fieldName)
	}
	if !resp.Success() {
		return errors.Errorf("update bitable field %s: code=%d msg=%s", fieldName, resp.Code, resp.Msg)
	}
	return nil
}

func (l *DispatchLogic) createBitableRecord(appToken, tableID string, artifact *ent.MediaArtifact, match *ent.Match, roundNo int, role string) (string, *string, error) {
	resp, err := l.svcCtx.Lark.Bitable.V1.AppTableRecord.Create(l.ctx, larkbitable.NewCreateAppTableRecordReqBuilder().
		AppToken(appToken).
		TableId(tableID).
		ClientToken(bitableRecordClientToken(artifact)).
		AppTableRecord(larkbitable.NewAppTableRecordBuilder().
			Fields(bitableupload.RecordFields(match, roundNo, role)).
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

func bitableRecordClientToken(artifact *ent.MediaArtifact) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("rm-monitor-upload-task-%d-%d", artifact.ID, artifact.CreatedAt.UnixNano())))
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	id, err := uuid.FromBytes(b[:])
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
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
	tasks, err := l.svcCtx.DB.UploadTask.Query().
		Where(
			uploadtask.StatusEQ(uploadtask.StatusPENDING),
			uploadtask.BitableRecordIDNotNil(),
			uploadtask.BitableTableIDNotNil(),
			uploadtask.BitableAppTokenNotNil(),
		).
		Order(uploadtask.ByPriority(sql.OrderDesc()), uploadtask.ByCreatedAt()).
		Limit(limit).
		All(l.ctx)
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
				Name:                       jobName,
				App:                        "uploader-job",
				Image:                      l.svcCtx.Config.K8sJobConf.Image,
				Args:                       []string{"-f", "/etc/rm-monitor/config.yml", "-task", strconv.Itoa(task.ID)},
				MountPVC:                   true,
				RecordsPVC:                 l.svcCtx.Config.K8sJobConf.WithDefaults().RecordsPVC,
				CPU:                        "100m",
				Memory:                     "256Mi",
				MemLimit:                   "1Gi",
				DisableStorageNodeSelector: true,
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
