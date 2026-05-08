package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"hash/adler32"
	"io"
	"os"
	"path"
	"strconv"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
	"github.com/pkg/errors"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/ent/uploadtask"
	common "scutbot.cn/web/rm-monitor/pkg/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/larkcache"
	"scutbot.cn/web/rm-monitor/pkg/larklog"
	"scutbot.cn/web/rm-monitor/pkg/storagepath"
	"scutbot.cn/web/rm-monitor/uploader-job/internal/config"
)

var (
	configFile = flag.String("f", "etc/config.yml", "the config file")
	taskIDFlag = flag.Int("task", 0, "upload task id")
)

func init() {
	logx.MustSetup(logx.LogConf{ServiceName: "uploader-job", Mode: "console", Encoding: "plain"})
}

func main() {
	flag.Parse()
	if *taskIDFlag == 0 {
		logx.Error("task id is required")
		os.Exit(1)
	}
	var c config.Config
	conf.MustLoad(*configFile, &c)

	client, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	defer client.Close()

	redisClient := redis.MustNewRedis(c.RedisConf)
	larkClient := lark.NewClient(c.LarkConf.AppId, c.LarkConf.AppSecret,
		lark.WithHttpClient(resty.New().SetRetryCount(3).SetRetryWaitTime(time.Second).SetTimeout(30*time.Second).Client()),
		lark.WithEnableTokenCache(true),
		lark.WithTokenCache(larkcache.NewLarkCache(redisClient)),
		lark.WithLogger(larklog.NewLarkLog()))

	if err := run(context.Background(), client, redisClient, larkClient, c, *taskIDFlag); err != nil {
		logx.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, client *ent.Client, redisClient *redis.Redis, larkClient *lark.Client, c config.Config, taskID int) error {
	task, err := client.UploadTask.Get(ctx, taskID)
	if err != nil {
		return errors.Wrap(err, "get upload task")
	}
	uploadConf := c.UploadConf.WithDefaults()
	filePath := storagepath.Resolve(uploadConf.BaseDir, task.SourcePath)
	f, err := os.Open(filePath)
	if err != nil {
		_ = client.UploadTask.UpdateOneID(taskID).SetStatus(uploadtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return errors.Wrap(err, "open upload source")
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return errors.Wrap(err, "stat upload source")
	}

	if err := client.UploadTask.UpdateOneID(taskID).SetStatus(uploadtask.StatusRUNNING).SetStartedAt(time.Now()).Exec(ctx); err != nil {
		return errors.Wrap(err, "mark upload running")
	}
	if err := waitUploadSlot(ctx, redisClient, uploadConf); err != nil {
		return err
	}

	dir, name := path.Split(task.SourcePath)
	dirNode, err := ensureFolder(ctx, larkClient, uploadConf.RootNode, dir)
	if err != nil {
		_ = client.UploadTask.UpdateOneID(taskID).SetStatus(uploadtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return err
	}
	prepareResp, err := larkClient.Drive.File.UploadPrepare(ctx, larkdrive.NewUploadPrepareFileReqBuilder().
		FileUploadInfo(larkdrive.NewFileUploadInfoBuilder().
			FileName(name).
			ParentType(larkdrive.ParentTypeExplorer).
			ParentNode(dirNode).
			Size(int(stat.Size())).
			Build()).
		Build())
	if err != nil {
		_ = client.UploadTask.UpdateOneID(taskID).SetStatus(uploadtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return errors.Wrap(err, "prepare upload")
	}
	if !prepareResp.Success() {
		err := errors.Wrap(prepareResp, "prepare upload")
		_ = client.UploadTask.UpdateOneID(taskID).SetStatus(uploadtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return err
	}
	uploadID := *prepareResp.Data.UploadId
	for i := 0; i < *prepareResp.Data.BlockNum; i++ {
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
		req := larkdrive.NewUploadPartFileReqBuilder().Body(larkdrive.NewUploadPartFileReqBodyBuilder().
			UploadId(uploadID).
			Size(endSize - startSize).
			File(bytes.NewReader(content)).
			Checksum(strconv.Itoa(int(adler32.Checksum(content)))).
			Seq(i).
			Build()).Build()
		if err := uploadPartWithRetry(ctx, larkClient, req, uploadConf); err != nil {
			_ = client.UploadTask.UpdateOneID(taskID).SetStatus(uploadtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
			return err
		}
	}
	completeResp, err := larkClient.Drive.File.UploadFinish(ctx, larkdrive.NewUploadFinishFileReqBuilder().
		Body(larkdrive.NewUploadFinishFileReqBodyBuilder().
			UploadId(uploadID).
			BlockNum(*prepareResp.Data.BlockNum).
			Build()).
		Build())
	if err != nil {
		_ = client.UploadTask.UpdateOneID(taskID).SetStatus(uploadtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return errors.Wrap(err, "finish upload")
	}
	if !completeResp.Success() {
		err := errors.Wrap(completeResp, "finish upload")
		_ = client.UploadTask.UpdateOneID(taskID).SetStatus(uploadtask.StatusFAILED).SetErrorMessage(err.Error()).Exec(ctx)
		return err
	}
	fileToken := *completeResp.Data.FileToken
	fileURL := fmt.Sprintf("https://scutrobotlab.feishu.cn/drive/file/%s", fileToken)
	if err := client.UploadTask.UpdateOneID(taskID).
		SetStatus(uploadtask.StatusSUCCEEDED).
		SetCompletedAt(time.Now()).
		SetFileToken(fileToken).
		SetFileURL(fileURL).
		Exec(ctx); err != nil {
		return errors.Wrap(err, "mark upload succeeded")
	}
	return db.Notify(ctx, c.PostgresConf.DSN, db.UploadTaskChangedChannel, strconv.Itoa(taskID))
}

func waitUploadSlot(ctx context.Context, redisClient *redis.Redis, conf common.UploadConf) error {
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

func uploadPartWithRetry(ctx context.Context, client *lark.Client, req *larkdrive.UploadPartFileReq, conf common.UploadConf) error {
	retries := conf.PartRetries
	backoff := time.Duration(conf.RetryBackoff) * time.Second
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		resp, err := client.Drive.File.UploadPart(ctx, req)
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

func ensureFolder(ctx context.Context, client *lark.Client, root, filepath string) (string, error) {
	filepath = path.Clean("/" + filepath)
	if filepath == "/" || filepath == "." {
		return root, nil
	}
	current := root
	for _, name := range splitPath(filepath) {
		token, err := ensureChildFolder(ctx, client, current, name)
		if err != nil {
			return "", err
		}
		current = token
	}
	return current, nil
}

func splitPath(filepath string) []string {
	var out []string
	for _, part := range bytes.Split([]byte(filepath), []byte("/")) {
		if len(part) > 0 {
			out = append(out, string(part))
		}
	}
	return out
}

func ensureChildFolder(ctx context.Context, client *lark.Client, parent, name string) (string, error) {
	iter, err := client.Drive.V1.File.ListByIterator(ctx, larkdrive.NewListFileReqBuilder().FolderToken(parent).Build())
	if err != nil {
		return "", errors.Wrap(err, "list folder")
	}
	for ok, node, err := iter.Next(); ok && err == nil; ok, node, err = iter.Next() {
		if node.Name != nil && node.Type != nil && *node.Name == name && *node.Type == larkdrive.FileTypeFolder {
			return *node.Token, nil
		}
	}
	resp, err := client.Drive.V1.File.CreateFolder(ctx, larkdrive.NewCreateFolderFileReqBuilder().
		Body(larkdrive.NewCreateFolderFileReqBodyBuilder().Name(name).FolderToken(parent).Build()).
		Build())
	if err != nil {
		return "", errors.Wrap(err, "create folder")
	}
	if !resp.Success() {
		return "", errors.Wrap(resp, "create folder")
	}
	return *resp.Data.Token, nil
}
