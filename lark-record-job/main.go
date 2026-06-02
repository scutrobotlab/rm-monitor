package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"hash/adler32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkbitable "github.com/larksuite/oapi-sdk-go/v3/service/bitable/v1"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
	"github.com/pkg/errors"
	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/larkbitablerecord"
	"scutbot.cn/web/rm-monitor/ent/matchround"
	"scutbot.cn/web/rm-monitor/lark-record-job/internal/config"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/bitableupload"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/larkcache"
	"scutbot.cn/web/rm-monitor/pkg/larklog"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
)

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "lark-record-job", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	var c config.Config
	app.MustLoadConfig(*configFile, &c)

	var jobCtx jobcontract.LarkRecordContext
	if err := jobcontract.ContextFromEnv(&jobCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	if jobCtx.BaseDir == "" {
		jobCtx.BaseDir = c.UploadConf.WithDefaults().BaseDir
	}
	jobDir := larkRecordJobDir(jobCtx.BaseDir, jobCtx.SourcePath, jobCtx.MatchRoundID, jobCtx.Role)
	if err := jobcontract.WriteContext(jobDir, jobCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}

	dbClient, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	defer dbClient.Close()

	redisClient := redisx.MustNew(c.RedisConf.WithDefaults())
	defer redisClient.Close()
	larkClient := lark.NewClient(c.LarkConf.AppId, c.LarkConf.AppSecret,
		lark.WithHttpClient(resty.New().SetTimeout(30*time.Second).Client()),
		lark.WithEnableTokenCache(true),
		lark.WithTokenCache(larkcache.NewLarkCache(redisClient)),
		lark.WithLogger(larklog.NewLarkLog()))

	if err := run(context.Background(), dbClient, redisClient, larkClient, c, jobCtx, jobDir); err != nil {
		_ = jobcontract.WriteError(jobDir, "lark-record", 0, err)
		logx.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dbClient *ent.Client, redisClient *redisx.Client, larkClient *lark.Client, c config.Config, jobCtx jobcontract.LarkRecordContext, jobDir string) error {
	if jobCtx.MatchRoundID == 0 || strings.TrimSpace(jobCtx.Role) == "" {
		return errors.New("match_round_id and role are required")
	}
	uploadConf := c.UploadConf.WithDefaults()
	if jobCtx.BitableAppToken == "" {
		jobCtx.BitableAppToken = strings.TrimSpace(uploadConf.BitableAppToken)
	}
	if jobCtx.BitableAppToken == "" {
		return errors.New("bitable_app_token is required")
	}
	if jobCtx.AttachmentFieldName == "" {
		jobCtx.AttachmentFieldName = bitableupload.FieldAttachment
	}
	filePath := storagepath.Resolve(jobCtx.BaseDir, jobCtx.SourcePath)
	f, err := os.Open(filePath)
	if err != nil {
		return errors.Wrap(err, "open upload source")
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return errors.Wrap(err, "stat upload source")
	}

	tableID := strings.TrimSpace(jobCtx.BitableTableIDHint)
	if tableID == "" {
		tableName := strings.TrimSpace(jobCtx.BitableTableName)
		if tableName == "" {
			return errors.New("bitable_table_name is required when table_id hint is empty")
		}
		tableID, err = ensureTable(ctx, redisClient, larkClient, jobCtx.BitableAppToken, tableName)
		if err != nil {
			return err
		}
	} else if err := ensureTableFields(ctx, redisClient, larkClient, jobCtx.BitableAppToken, tableID); err != nil {
		return err
	}
	recordID := strings.TrimSpace(jobCtx.BitableRecordIDHint)
	recordURL := strings.TrimSpace(jobCtx.BitableRecordURL)
	if recordID == "" {
		recordID, recordURL, err = createBitableRecord(ctx, larkClient, jobCtx.BitableAppToken, tableID, jobCtx)
		if err != nil {
			return err
		}
	}
	if err := upsertLarkBitableRecord(ctx, dbClient, jobCtx, tableID, recordID, recordURL, "", 0); err != nil {
		return err
	}
	name := filepath.Base(jobCtx.SourcePath)
	if err := waitUploadSlot(ctx, redisClient, uploadConf); err != nil {
		return err
	}
	prepareResp, err := uploadPrepareWithRetry(ctx, larkClient, name, jobCtx.BitableAppToken, int(stat.Size()), uploadConf)
	if err != nil {
		return err
	}
	uploadID := *prepareResp.Data.UploadId
	for i := 0; i < *prepareResp.Data.BlockNum; i++ {
		if err := waitUploadSlot(ctx, redisClient, uploadConf); err != nil {
			return err
		}
		startSize := i * *prepareResp.Data.BlockSize
		endSize := startSize + *prepareResp.Data.BlockSize
		if endSize > int(stat.Size()) {
			endSize = int(stat.Size())
		}
		reader := io.NewSectionReader(f, int64(startSize), int64(endSize-startSize))
		content, err := io.ReadAll(reader)
		if err != nil {
			return errors.Wrap(err, "read part")
		}
		part := uploadPart{
			uploadID: uploadID,
			seq:      i,
			size:     endSize - startSize,
			checksum: strconv.Itoa(int(adler32.Checksum(content))),
			content:  content,
		}
		if err := uploadPartWithRetry(ctx, larkClient, part, uploadConf); err != nil {
			return err
		}
	}
	if err := waitUploadSlot(ctx, redisClient, uploadConf); err != nil {
		return err
	}
	completeResp, err := uploadFinishWithRetry(ctx, larkClient, uploadID, *prepareResp.Data.BlockNum, uploadConf)
	if err != nil {
		return err
	}
	fileToken := *completeResp.Data.FileToken
	if err := updateBitableAttachmentWithRetry(ctx, larkClient, jobCtx.BitableAppToken, tableID, recordID, jobCtx.AttachmentFieldName, fileToken, name, uploadConf); err != nil {
		return err
	}
	if err := upsertLarkBitableRecord(ctx, dbClient, jobCtx, tableID, recordID, recordURL, fileToken, stat.Size()); err != nil {
		return err
	}
	result := jobcontract.LarkRecordResult{
		Schema:              "rm-monitor/lark-record-result/v1",
		MatchID:             jobCtx.MatchID,
		MatchRoundID:        jobCtx.MatchRoundID,
		Role:                jobCtx.Role,
		BitableAppToken:     jobCtx.BitableAppToken,
		BitableTableID:      tableID,
		BitableRecordID:     recordID,
		AttachmentFileToken: fileToken,
		BitableRecordURL:    recordURL,
		FileSize:            stat.Size(),
		CompletedAt:         time.Now(),
	}
	if err := jobcontract.WriteTempResult(result); err != nil {
		return err
	}
	return jobcontract.WriteArgoOutputs(map[string]any{
		"attachment_file_token": result.AttachmentFileToken,
		"bitable_table_id":      result.BitableTableID,
		"bitable_record_id":     result.BitableRecordID,
		"bitable_record_url":    result.BitableRecordURL,
		"file_size":             result.FileSize,
	})
}

func waitUploadSlot(ctx context.Context, redisClient *redisx.Client, conf common.UploadConf) error {
	key := fmt.Sprintf("%s:%s", conf.RateLimitKey, time.Now().Format("200601021504"))
	for {
		n, err := redisClient.IncrCtx(ctx, key)
		if err != nil {
			return errors.Wrap(err, "lark upload rate limit")
		}
		if n == 1 {
			_ = redisClient.ExpireCtx(ctx, key, 90)
		}
		if n <= int64(conf.RateLimitPerMinute) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func larkRecordJobDir(baseDir, sourcePath string, roundID int, role string) string {
	name := fmt.Sprintf("lark-record-%d-%s", roundID, safeName(role))
	return filepath.Join(filepath.Dir(storagepath.Resolve(baseDir, sourcePath)), jobcontract.DirName, name)
}

func uploadPrepareWithRetry(ctx context.Context, client *lark.Client, name, appToken string, size int, conf common.UploadConf) (*larkdrive.UploadPrepareMediaResp, error) {
	var out *larkdrive.UploadPrepareMediaResp
	err := retryLarkUpload(ctx, conf, func() error {
		resp, err := client.Drive.Media.UploadPrepare(ctx, larkdrive.NewUploadPrepareMediaReqBuilder().
			MediaUploadInfo(larkdrive.NewMediaUploadInfoBuilder().
				FileName(name).
				ParentType(larkdrive.ParentTypeUploadPrepareMediaBitableFile).
				ParentNode(appToken).
				Size(size).
				Build()).
			Build())
		if err != nil {
			return errors.Wrap(err, "prepare upload")
		}
		if !resp.Success() {
			return errors.Wrap(resp, "prepare upload")
		}
		out = resp
		return nil
	})
	return out, err
}

type uploadPart struct {
	uploadID string
	seq      int
	size     int
	checksum string
	content  []byte
}

func (p uploadPart) request() *larkdrive.UploadPartMediaReq {
	return larkdrive.NewUploadPartMediaReqBuilder().Body(larkdrive.NewUploadPartMediaReqBodyBuilder().
		UploadId(p.uploadID).
		Size(p.size).
		File(bytes.NewReader(p.content)).
		Checksum(p.checksum).
		Seq(p.seq).
		Build()).Build()
}

func uploadPartWithRetry(ctx context.Context, client *lark.Client, part uploadPart, conf common.UploadConf) error {
	return retryLarkUpload(ctx, conf, func() error {
		resp, err := client.Drive.Media.UploadPart(ctx, part.request())
		if err == nil && resp.Success() {
			return nil
		}
		if err != nil {
			return err
		}
		return errors.Wrap(resp, "upload part failed")
	})
}

func uploadFinishWithRetry(ctx context.Context, client *lark.Client, uploadID string, blockNum int, conf common.UploadConf) (*larkdrive.UploadFinishMediaResp, error) {
	var out *larkdrive.UploadFinishMediaResp
	err := retryLarkUpload(ctx, conf, func() error {
		resp, err := client.Drive.Media.UploadFinish(ctx, larkdrive.NewUploadFinishMediaReqBuilder().
			Body(larkdrive.NewUploadFinishMediaReqBodyBuilder().
				UploadId(uploadID).
				BlockNum(blockNum).
				Build()).
			Build())
		if err != nil {
			return errors.Wrap(err, "finish upload")
		}
		if !resp.Success() {
			return errors.Wrap(resp, "finish upload")
		}
		out = resp
		return nil
	})
	return out, err
}

func retryLarkUpload(ctx context.Context, conf common.UploadConf, f func() error) error {
	retries := conf.PartRetries
	backoff := time.Duration(conf.RetryBackoff) * time.Second
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		lastErr = f()
		if lastErr == nil {
			return nil
		}
		if attempt < retries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff * time.Duration(attempt+1)):
			}
		}
	}
	return lastErr
}

func updateBitableAttachmentWithRetry(ctx context.Context, client *lark.Client, appToken, tableID, recordID, fieldName, fileToken, name string, conf common.UploadConf) error {
	return retryLarkUpload(ctx, conf, func() error {
		return updateBitableAttachment(ctx, client, appToken, tableID, recordID, fieldName, fileToken, name)
	})
}

func updateBitableAttachment(ctx context.Context, client *lark.Client, appToken, tableID, recordID, fieldName, fileToken, name string) error {
	if fieldName == "" {
		fieldName = bitableupload.FieldAttachment
	}
	resp, err := client.Bitable.V1.AppTableRecord.Update(ctx, larkbitable.NewUpdateAppTableRecordReqBuilder().
		AppToken(appToken).
		TableId(tableID).
		RecordId(recordID).
		AppTableRecord(larkbitable.NewAppTableRecordBuilder().
			Fields(map[string]interface{}{
				fieldName: bitableupload.AttachmentValue(fileToken, name),
			}).
			Build()).
		Build())
	if err != nil {
		return errors.Wrap(err, "update bitable attachment")
	}
	if !resp.Success() {
		return errors.Wrap(resp, "update bitable attachment")
	}
	return nil
}

const tableCacheTTL = 24 * 3600
const tableLockTTL = 30

func ensureTable(ctx context.Context, redisClient *redisx.Client, client *lark.Client, appToken, tableName string) (string, error) {
	cacheKey := fmt.Sprintf("rm-monitor:bitable:table:%s:%s", appToken, tableName)
	if tableID, err := redisClient.GetCtx(ctx, cacheKey); err != nil {
		return "", errors.Wrap(err, "get bitable table cache")
	} else if tableID != "" {
		if err := ensureTableFields(ctx, redisClient, client, appToken, tableID); err != nil {
			return "", err
		}
		return tableID, nil
	}
	if tableID, err := findTable(ctx, client, appToken, tableName); err != nil {
		return "", err
	} else if tableID != "" {
		if err := ensureTableFields(ctx, redisClient, client, appToken, tableID); err != nil {
			return "", err
		}
		_ = redisClient.SetexCtx(ctx, cacheKey, tableID, tableCacheTTL)
		return tableID, nil
	}
	lockKey := cacheKey + ":lock"
	locked, err := redisClient.SetNXCtx(ctx, lockKey, "1", tableLockTTL)
	if err != nil {
		return "", errors.Wrap(err, "lock bitable table creation")
	}
	if !locked {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			if tableID, err := findTable(ctx, client, appToken, tableName); err != nil {
				return "", err
			} else if tableID != "" {
				_ = redisClient.SetexCtx(ctx, cacheKey, tableID, tableCacheTTL)
				if err := ensureTableFields(ctx, redisClient, client, appToken, tableID); err != nil {
					return "", err
				}
				return tableID, nil
			}
		}
		return "", errors.Errorf("wait bitable table creation timeout: %s", tableName)
	}
	defer func() { _ = redisClient.DelCtx(context.Background(), lockKey) }()
	resp, err := client.Bitable.V1.AppTable.Create(ctx, larkbitable.NewCreateAppTableReqBuilder().
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
	tableID := *resp.Data.TableId
	if err := ensureTableFields(ctx, redisClient, client, appToken, tableID); err != nil {
		return "", err
	}
	_ = redisClient.SetexCtx(ctx, cacheKey, tableID, tableCacheTTL)
	return tableID, nil
}

func findTable(ctx context.Context, client *lark.Client, appToken, tableName string) (string, error) {
	pageToken := ""
	for {
		builder := larkbitable.NewListAppTableReqBuilder().AppToken(appToken).PageSize(100)
		if pageToken != "" {
			builder.PageToken(pageToken)
		}
		resp, err := client.Bitable.V1.AppTable.List(ctx, builder.Build())
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

func ensureTableFields(ctx context.Context, redisClient *redisx.Client, client *lark.Client, appToken, tableID string) error {
	lockKey := fmt.Sprintf("rm-monitor:bitable:fields:%s:%s:lock", appToken, tableID)
	locked, err := redisClient.SetNXCtx(ctx, lockKey, "1", tableLockTTL)
	if err != nil {
		return errors.Wrap(err, "lock bitable fields")
	}
	if !locked {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			existing, err := listFields(ctx, client, appToken, tableID)
			if err != nil {
				return err
			}
			if requiredFieldsReady(existing) {
				return nil
			}
		}
		return errors.Errorf("wait bitable field creation timeout: %s", tableID)
	}
	defer func() { _ = redisClient.DelCtx(context.Background(), lockKey) }()
	existing, err := listFields(ctx, client, appToken, tableID)
	if err != nil {
		return err
	}
	for name, fieldType := range requiredBitableFields() {
		if existingField, ok := existing[name]; ok {
			if existingField.Type == fieldType {
				continue
			}
			if err := updateFieldType(ctx, client, appToken, tableID, existingField.ID, name, fieldType); err != nil {
				return err
			}
			continue
		}
		resp, err := client.Bitable.V1.AppTableField.Create(ctx, larkbitable.NewCreateAppTableFieldReqBuilder().
			AppToken(appToken).
			TableId(tableID).
			AppTableField(larkbitable.NewAppTableFieldBuilder().FieldName(name).Type(fieldType).Build()).
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

type bitableFieldInfo struct {
	ID   string
	Type int
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

func requiredFieldsReady(existing map[string]bitableFieldInfo) bool {
	for name, fieldType := range requiredBitableFields() {
		existingField, ok := existing[name]
		if !ok || existingField.Type != fieldType {
			return false
		}
	}
	return true
}

func listFields(ctx context.Context, client *lark.Client, appToken, tableID string) (map[string]bitableFieldInfo, error) {
	fields := make(map[string]bitableFieldInfo)
	pageToken := ""
	for {
		builder := larkbitable.NewListAppTableFieldReqBuilder().AppToken(appToken).TableId(tableID).PageSize(100)
		if pageToken != "" {
			builder.PageToken(pageToken)
		}
		resp, err := client.Bitable.V1.AppTableField.List(ctx, builder.Build())
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

func updateFieldType(ctx context.Context, client *lark.Client, appToken, tableID, fieldID, fieldName string, fieldType int) error {
	resp, err := client.Bitable.V1.AppTableField.Update(ctx, larkbitable.NewUpdateAppTableFieldReqBuilder().
		AppToken(appToken).
		TableId(tableID).
		FieldId(fieldID).
		AppTableField(larkbitable.NewAppTableFieldBuilder().FieldName(fieldName).Type(fieldType).Build()).
		Build())
	if err != nil {
		return errors.Wrapf(err, "update bitable field %s", fieldName)
	}
	if !resp.Success() {
		return errors.Errorf("update bitable field %s: code=%d msg=%s", fieldName, resp.Code, resp.Msg)
	}
	return nil
}

func createBitableRecord(ctx context.Context, client *lark.Client, appToken, tableID string, jobCtx jobcontract.LarkRecordContext) (string, string, error) {
	fields := jobCtx.RecordFields
	if len(fields) == 0 {
		return "", "", errors.New("record_fields is required")
	}
	resp, err := client.Bitable.V1.AppTableRecord.Create(ctx, larkbitable.NewCreateAppTableRecordReqBuilder().
		AppToken(appToken).
		TableId(tableID).
		ClientToken(bitableRecordClientToken(jobCtx)).
		AppTableRecord(larkbitable.NewAppTableRecordBuilder().Fields(fields).Build()).
		Build())
	if err != nil {
		return "", "", errors.Wrap(err, "create bitable record")
	}
	if !resp.Success() || resp.Data == nil || resp.Data.Record == nil || resp.Data.Record.RecordId == nil {
		return "", "", errors.Wrap(resp, "create bitable record")
	}
	record := resp.Data.Record
	url := ""
	if record.RecordUrl != nil && *record.RecordUrl != "" {
		url = *record.RecordUrl
	} else if record.SharedUrl != nil && *record.SharedUrl != "" {
		url = *record.SharedUrl
	} else {
		url = fmt.Sprintf("https://scutrobotlab.feishu.cn/base/%s?table=%s&record=%s", appToken, tableID, *record.RecordId)
	}
	return *record.RecordId, url, nil
}

func bitableRecordClientToken(jobCtx jobcontract.LarkRecordContext) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("rm-monitor-lark-record-%s-%d-%s-%s", jobCtx.MatchID, jobCtx.MatchRoundID, jobCtx.Role, jobCtx.SourcePath)))
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

func upsertLarkBitableRecord(ctx context.Context, client *ent.Client, jobCtx jobcontract.LarkRecordContext, tableID, recordID, recordURL, fileToken string, fileSize int64) error {
	create := client.LarkBitableRecord.Create().
		SetMatchRoundID(jobCtx.MatchRoundID).
		SetRole(jobCtx.Role).
		SetAppToken(jobCtx.BitableAppToken).
		SetTableID(tableID).
		SetRecordID(recordID).
		SetSourcePath(jobCtx.SourcePath).
		SetFileSize(fileSize)
	if strings.TrimSpace(fileToken) != "" {
		create.SetAttachmentFileToken(fileToken)
	}
	if strings.TrimSpace(recordURL) != "" {
		create.SetRecordURL(recordURL)
	}
	if _, err := create.Save(ctx); err != nil {
		if !ent.IsConstraintError(err) {
			return errors.Wrap(err, "create lark bitable record")
		}
		update := client.LarkBitableRecord.Update().
			Where(larkbitablerecord.HasMatchRoundWith(matchround.ID(jobCtx.MatchRoundID)), larkbitablerecord.RoleEQ(jobCtx.Role)).
			SetAppToken(jobCtx.BitableAppToken).
			SetTableID(tableID).
			SetRecordID(recordID).
			SetSourcePath(jobCtx.SourcePath).
			SetFileSize(fileSize)
		if strings.TrimSpace(fileToken) != "" {
			update.SetAttachmentFileToken(fileToken)
		}
		if strings.TrimSpace(recordURL) != "" {
			update.SetRecordURL(recordURL)
		}
		if _, err := update.Save(ctx); err != nil {
			return errors.Wrap(err, "update lark bitable record")
		}
	}
	return nil
}

func safeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "role"
	}
	return out
}
