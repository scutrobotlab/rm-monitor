package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"hash/adler32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkbitable "github.com/larksuite/oapi-sdk-go/v3/service/bitable/v1"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
	"github.com/pkg/errors"
	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/pkg/app"
	"scutbot.cn/web/rm-monitor/pkg/bitableupload"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/jobcontract"
	"scutbot.cn/web/rm-monitor/pkg/larkcache"
	"scutbot.cn/web/rm-monitor/pkg/larklog"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
	"scutbot.cn/web/rm-monitor/uploader-job/internal/config"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
)

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "uploader-job", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	var c config.Config
	app.MustLoadConfig(*configFile, &c)

	var jobCtx jobcontract.UploadContext
	if err := jobcontract.ContextFromEnv(&jobCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	if jobCtx.BaseDir == "" {
		jobCtx.BaseDir = c.UploadConf.WithDefaults().BaseDir
	}
	jobDir := uploadJobDir(jobCtx.BaseDir, jobCtx.SourcePath, jobCtx.UploadTaskID)
	if err := jobcontract.WriteContext(jobDir, jobCtx); err != nil {
		logx.Error(err)
		os.Exit(1)
	}

	redisClient := redisx.MustNew(c.RedisConf.WithDefaults())
	defer redisClient.Close()
	larkClient := lark.NewClient(c.LarkConf.AppId, c.LarkConf.AppSecret,
		lark.WithHttpClient(resty.New().SetRetryCount(3).SetRetryWaitTime(time.Second).SetTimeout(30*time.Second).Client()),
		lark.WithEnableTokenCache(true),
		lark.WithTokenCache(larkcache.NewLarkCache(redisClient)),
		lark.WithLogger(larklog.NewLarkLog()))

	if err := run(context.Background(), redisClient, larkClient, c, jobCtx, jobDir); err != nil {
		_ = jobcontract.WriteError(jobDir, "upload", jobCtx.UploadTaskID, err)
		logx.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, redisClient *redisx.Client, larkClient *lark.Client, c config.Config, jobCtx jobcontract.UploadContext, jobDir string) error {
	if jobCtx.UploadTaskID == 0 {
		return errors.New("upload_task_id is required")
	}
	uploadConf := c.UploadConf.WithDefaults()
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

	if err := waitUploadSlot(ctx, redisClient, uploadConf); err != nil {
		return err
	}

	if jobCtx.BitableAppToken == "" || jobCtx.BitableTableID == "" || jobCtx.BitableRecordID == "" {
		return errors.New("missing bitable upload context")
	}
	name := filepath.Base(jobCtx.SourcePath)
	prepareResp, err := larkClient.Drive.Media.UploadPrepare(ctx, larkdrive.NewUploadPrepareMediaReqBuilder().
		MediaUploadInfo(larkdrive.NewMediaUploadInfoBuilder().
			FileName(name).
			ParentType(larkdrive.ParentTypeUploadPrepareMediaBitableFile).
			ParentNode(jobCtx.BitableAppToken).
			Size(int(stat.Size())).
			Build()).
		Build())
	if err != nil {
		return errors.Wrap(err, "prepare upload")
	}
	if !prepareResp.Success() {
		return errors.Wrap(prepareResp, "prepare upload")
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
	completeResp, err := larkClient.Drive.Media.UploadFinish(ctx, larkdrive.NewUploadFinishMediaReqBuilder().
		Body(larkdrive.NewUploadFinishMediaReqBodyBuilder().
			UploadId(uploadID).
			BlockNum(*prepareResp.Data.BlockNum).
			Build()).
		Build())
	if err != nil {
		return errors.Wrap(err, "finish upload")
	}
	if !completeResp.Success() {
		return errors.Wrap(completeResp, "finish upload")
	}
	fileToken := *completeResp.Data.FileToken
	if err := updateBitableAttachment(ctx, larkClient, jobCtx.BitableAppToken, jobCtx.BitableTableID, jobCtx.BitableRecordID, fileToken, name); err != nil {
		return err
	}
	return jobcontract.WriteResult(jobDir, jobcontract.UploadResult{
		Schema:              "rm-monitor/upload-result/v1",
		UploadTaskID:        jobCtx.UploadTaskID,
		AttachmentFileToken: fileToken,
		BitableRecordURL:    jobCtx.BitableRecordURL,
		FileSize:            stat.Size(),
		CompletedAt:         time.Now(),
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

func uploadJobDir(baseDir, sourcePath string, taskID int) string {
	return filepath.Join(filepath.Dir(storagepath.Resolve(baseDir, sourcePath)), jobcontract.DirName, fmt.Sprintf("upload-%d", taskID))
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
	retries := conf.PartRetries
	backoff := time.Duration(conf.RetryBackoff) * time.Second
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		resp, err := client.Drive.Media.UploadPart(ctx, part.request())
		if err == nil && resp.Success() {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = errors.Wrap(resp, "upload part failed")
		}
		if attempt < retries {
			time.Sleep(backoff * time.Duration(attempt+1))
		}
	}
	return lastErr
}

func updateBitableAttachment(ctx context.Context, client *lark.Client, appToken, tableID, recordID, fileToken, name string) error {
	resp, err := client.Bitable.V1.AppTableRecord.Update(ctx, larkbitable.NewUpdateAppTableRecordReqBuilder().
		AppToken(appToken).
		TableId(tableID).
		RecordId(recordID).
		AppTableRecord(larkbitable.NewAppTableRecordBuilder().
			Fields(map[string]interface{}{
				bitableupload.FieldAttachment: bitableupload.AttachmentValue(fileToken, name),
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
