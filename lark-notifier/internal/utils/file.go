package utils

import (
	"context"
	"fmt"
	"path"
	"strings"

	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/pkg/logc"

	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
)

func GetFolderTokenWithCache(ctx context.Context, svcCtx *svc.ServiceContext, parentNode string, filepath string) (string, error) {
	key := fmt.Sprintf("rm-monitor:lark:folder_token:%s:%s", parentNode, filepath)
	token, err := svcCtx.RedisClient.GetCtx(ctx, key)
	if err != nil {
		return "", errors.Wrap(err, "lark get folder token failed")
	}

	if token == "" {
		token, err = GetFolderToken(ctx, svcCtx, parentNode, filepath)
		if err != nil {
			return "", errors.Wrap(err, "lark get folder token failed")
		}

		err = svcCtx.RedisClient.SetexCtx(ctx, key, token, 6*60*60)
		if err != nil {
			return "", errors.Wrap(err, "lark set folder token failed")
		}
	}

	return token, nil
}

func GetFolderToken(ctx context.Context, svcCtx *svc.ServiceContext, parentNode string, filepath string) (string, error) {
	filepath = strings.TrimPrefix(filepath, "/")

	if filepath == "" {
		return parentNode, nil
	}

	dir, name := path.Split(filepath)
	if dir != "" {
		dir = strings.TrimSuffix(dir, `/`)
		dirNode, err := GetFolderToken(ctx, svcCtx, parentNode, dir)
		if err != nil {
			return "", errors.Wrap(err, "lark get folder token failed")
		}
		return GetFolderToken(ctx, svcCtx, dirNode, name)
	} else {
		req := larkdrive.NewListFileReqBuilder().
			OrderBy(`EditedTime`).
			Direction(`DESC`).
			FolderToken(parentNode).
			Build()

		// 发起请求
		resp, err := svcCtx.LarkClient.Drive.V1.File.ListByIterator(ctx, req)
		if err != nil {
			return "", errors.Wrap(err, "lark list file failed")
		}
		var ok bool
		var node *larkdrive.File
		var resultToken string
		for ok, node, err = resp.Next(); ok && err == nil; ok, node, err = resp.Next() {
			if *node.Name == name && *node.Type == larkdrive.FileTypeFolder {
				resultToken = *node.Token
				break
			}
		}
		if err != nil {
			return "", errors.Wrap(err, "lark list file failed")
		}

		if resultToken == "" {
			logc.Debugf(ctx, "creating folder %s under %s", name, parentNode)

			req := larkdrive.NewCreateFolderFileReqBuilder().
				Body(larkdrive.NewCreateFolderFileReqBodyBuilder().
					Name(name).
					FolderToken(parentNode).
					Build()).
				Build()

			resp, err := svcCtx.LarkClient.Drive.V1.File.CreateFolder(ctx, req)
			if err != nil {
				return "", errors.Wrap(err, "lark create folder failed")
			}

			if !resp.Success() {
				return "", errors.Wrapf(resp, "lark create folder failed")
			}

			resultToken = *resp.Data.Token
		}

		return resultToken, nil
	}
}
