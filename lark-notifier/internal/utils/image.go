package utils

import (
	"context"
	"fmt"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/singleflight"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
)

var imageGroup singleflight.Group

func GetImageKey(ctx context.Context, svcCtx *svc.ServiceContext, imageUrl string) (string, error) {
	key := fmt.Sprintf("rm-monitor:image-key:%s", imageUrl)
	imageKey, err := svcCtx.RedisClient.GetCtx(ctx, key)
	if err != nil {
		return "", errors.Wrap(err, "redis get image key")
	}

	if imageKey != "" {
		return imageKey, nil
	}

	v, err, _ := imageGroup.Do(key, func() (any, error) {
		return fetchImageKey(ctx, svcCtx, imageUrl)
	})
	if err != nil {
		return "", err
	}
	imageKey = v.(string)
	if err := svcCtx.RedisClient.SetexCtx(ctx, key, imageKey, 30*24*60*60); err != nil {
		return "", errors.Wrap(err, "redis set image key")
	}

	return imageKey, nil
}

func fetchImageKey(ctx context.Context, svcCtx *svc.ServiceContext, imageUrl string) (string, error) {
	file, err := svcCtx.RestyClient.R().Get(imageUrl)
	if err != nil {
		return "", errors.Wrap(err, "http get image")
	}
	if file.IsError() {
		return "", errors.Wrap(err, "http get image")
	}

	defer file.Body.Close()

	if err := svcCtx.RateLimiter.Wait(ctx, ""); err != nil {
		return "", err
	}
	req := larkim.NewCreateImageReqBuilder().
		Body(larkim.NewCreateImageReqBodyBuilder().
			ImageType(larkim.ImageTypeMessage).
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

	return *resp.Data.ImageKey, nil
}
