package utils

import (
	"context"
	"fmt"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
)

func GetImageKey(ctx context.Context, svcCtx *svc.ServiceContext, imageUrl string) (string, error) {
	key := fmt.Sprintf("rm-monitor:image-key:%s", imageUrl)
	imageKey, err := svcCtx.RedisClient.GetCtx(ctx, key)
	if err != nil {
		return "", errors.Wrap(err, "redis get image key")
	}

	if imageKey != "" {
		return imageKey, nil
	}

	file, err := svcCtx.HttpClient.Get(imageUrl)
	if err != nil {
		return "", errors.Wrap(err, "http get image")
	}
	defer file.Body.Close()

	req := larkim.NewCreateImageReqBuilder().
		Body(larkim.NewCreateImageReqBodyBuilder().
			ImageType(`message`).
			Image(file.Body).
			Build()).
		Build()

	// 发起请求
	resp, err := svcCtx.LarkClient.Im.V1.Image.Create(ctx, req)
	if err != nil {
		return "", errors.Wrap(err, "lark create image")
	}

	if !resp.Success() {
		return "", errors.Wrap(resp, "lark create image")
	}

	imageKey = *resp.Data.ImageKey
	if err := svcCtx.RedisClient.SetCtx(ctx, key, imageKey); err != nil {
		return "", errors.Wrap(err, "redis set image key")
	}

	return imageKey, nil
}
