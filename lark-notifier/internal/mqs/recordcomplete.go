package mqs

import (
	"bytes"
	"context"
	"fmt"
	"hash/adler32"
	"io"
	"os"
	"path"
	"strconv"

	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"github.com/zeromicro/go-zero/core/logx"

	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/utils"
	"scutbot.cn/web/rm-monitor/recorder/types"
)

type RecordCompletedLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewRecordCompletedLogic(ctx context.Context, svcCtx *svc.ServiceContext) Consumer[types.RecordCompletedEvent] {
	return &RecordCompletedLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *RecordCompletedLogic) Consume(key string, m types.RecordCompletedEvent) error {
	l.Infof("record completed: %s %s", m.Match.Id, m.Role)
	f, err := os.OpenFile(path.Join(l.svcCtx.Config.RecordConf.BaseDir, m.Path), os.O_RDONLY, os.ModePerm)
	if err != nil {
		return errors.Wrap(err, "failed to open file")
	}
	stat, err := f.Stat()
	if err != nil {
		return errors.Wrap(err, "failed to stat file")
	}

	// upload file
	dir, name := path.Split(m.Path)
	dirNode, err := utils.GetFolderTokenWithCache(l.ctx, l.svcCtx, l.svcCtx.Config.RecordConf.RootNode, dir)
	if err != nil {
		return errors.Wrap(err, "failed to get folder token")
	}

	prepareReq := larkdrive.NewUploadPrepareFileReqBuilder().
		FileUploadInfo(larkdrive.NewFileUploadInfoBuilder().
			FileName(name).
			ParentType(larkdrive.ParentTypeExplorer).
			ParentNode(dirNode).
			Size(int(stat.Size())).
			Build()).
		Build()

	prepareResp, err := l.svcCtx.LarkClient.Drive.File.UploadPrepare(l.ctx, prepareReq)
	if err != nil {
		return errors.Wrap(err, "failed to prepare upload")
	}
	if !prepareResp.Success() {
		return errors.Wrap(prepareResp, "failed to prepare upload")
	}

	uploadId := *prepareResp.Data.UploadId
	l.Logger.Infof("starting lark upload to %s size %d with id %s", dirNode, stat.Size(), uploadId)

	for i := 0; i < *prepareResp.Data.BlockNum; i++ {
		startSize := i * *prepareResp.Data.BlockSize
		endSize := startSize + *prepareResp.Data.BlockSize
		if endSize > int(stat.Size()) {
			endSize = int(stat.Size())
		}
		reader := io.NewSectionReader(f, int64(startSize), int64(endSize-startSize))
		content, err := io.ReadAll(reader)
		if err != nil {
			return errors.Wrap(err, "failed to read file")
		}
		checksum := adler32.Checksum(content)

		uploadReq := larkdrive.NewUploadPartFileReqBuilder().Body(larkdrive.NewUploadPartFileReqBodyBuilder().
			UploadId(uploadId).
			Size(endSize - startSize).
			File(bytes.NewReader(content)).
			Checksum(strconv.Itoa(int(checksum))).
			Seq(i).
			Build()).Build()
		uploadResp, err := l.svcCtx.LarkClient.Drive.File.UploadPart(l.ctx, uploadReq)
		if err != nil {
			return errors.Wrap(err, "failed to upload part")
		}
		if !uploadResp.Success() {
			return errors.Wrap(uploadResp, "failed to upload part")
		}

		l.Logger.Infof("uploaded part %d/%d on %s id %s", i+1, *prepareResp.Data.BlockNum, m.Path, uploadId)
	}

	completeReq := larkdrive.NewUploadFinishFileReqBuilder().Body(larkdrive.NewUploadFinishFileReqBodyBuilder().
		UploadId(uploadId).
		BlockNum(*prepareResp.Data.BlockNum).
		Build()).Build()
	completeResp, err := l.svcCtx.LarkClient.Drive.File.UploadFinish(l.ctx, completeReq)
	if err != nil {
		return errors.Wrap(err, "failed to complete upload")
	}
	if !completeResp.Success() {
		return errors.Wrap(completeResp, "failed to complete upload")
	}

	fileUrl := fmt.Sprintf("https://scutrobotlab.feishu.cn/drive/file/%s", *completeResp.Data.FileToken)

	l.Logger.Infof("completed upload %s %s. url", m.Path, uploadId, fileUrl)

	messageIds, err := utils.GetMatchMessageIds(l.ctx, l.svcCtx, m.Match.Id)
	if err != nil {
		return errors.Wrap(err, "failed to get message ids")
	}

	for _, messageId := range messageIds {
		req := larkim.NewReplyMessageReqBuilder().
			Body(larkim.NewReplyMessageReqBodyBuilder().
				Content(fmt.Sprintf(`{"text":"%s"}`, fileUrl)).
				MsgType(`text`).
				ReplyInThread(true).
				Uuid(m.Path).
				Build()).
			MessageId(messageId).
			Build()

		resp, err := l.svcCtx.LarkClient.Im.V1.Message.Reply(l.ctx, req)
		if err != nil {
			l.Errorf("failed to update message: %v", err)
		}
		if !resp.Success() {
			l.Error(errors.Wrapf(resp, "failed to update message %+v", req))
		}
	}

	return nil
}
