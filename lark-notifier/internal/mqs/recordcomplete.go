package mqs

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
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
	//f, err := os.OpenFile(path.Join(l.svcCtx.Config.RecordConf.BaseDir, m.Path), os.O_RDONLY, os.ModePerm)
	//if err != nil {
	//	return errors.Wrap(err, "failed to open file")
	//}
	//stat, err := f.Stat()
	//if err != nil {
	//	return errors.Wrap(err, "failed to stat file")
	//}
	//
	//// upload file
	//dir, name := path.Split(m.Path)
	//dirNode, err := utils.GetFolderTokenWithCache(l.ctx, l.svcCtx, l.svcCtx.Config.RecordConf.RootNode, dir)
	//if err != nil {
	//	return errors.Wrap(err, "failed to get folder token")
	//}
	//
	//prepareReq := larkdrive.NewUploadPrepareFileReqBuilder().
	//	FileUploadInfo(larkdrive.NewFileUploadInfoBuilder().
	//		FileName(name).
	//		ParentType(larkdrive.ParentTypeExplorer).
	//		ParentNode(dirNode).
	//		Size(int(stat.Size())).
	//		Build()).
	//	Build()

	return nil
}
