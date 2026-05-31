package utils

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/singleflight"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
)

var imageGroup singleflight.Group

func GetImageKey(ctx context.Context, svcCtx *svc.ServiceContext, imageUrl string) (string, error) {
	if strings.TrimSpace(imageUrl) == "" {
		return "", nil
	}
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

func GetLocalImageKey(ctx context.Context, svcCtx *svc.ServiceContext, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", errors.Wrap(err, "read local image")
	}
	sum := sha256.Sum256(raw)
	cacheKey := fmt.Sprintf("rm-monitor:image-key:file-sha256:%s", hex.EncodeToString(sum[:]))
	imageKey, err := svcCtx.RedisClient.GetCtx(ctx, cacheKey)
	if err != nil {
		return "", errors.Wrap(err, "redis get local image key")
	}
	if imageKey != "" {
		return imageKey, nil
	}
	v, err, _ := imageGroup.Do(cacheKey, func() (any, error) {
		return uploadImage(ctx, svcCtx, bytes.NewReader(raw))
	})
	if err != nil {
		return "", err
	}
	imageKey = v.(string)
	if err := svcCtx.RedisClient.SetexCtx(ctx, cacheKey, imageKey, 30*24*60*60); err != nil {
		return "", errors.Wrap(err, "redis set local image key")
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

	return uploadImage(ctx, svcCtx, file.Body)
}

func uploadImage(ctx context.Context, svcCtx *svc.ServiceContext, reader io.Reader) (string, error) {
	if err := svcCtx.RateLimiter.Wait(ctx, ""); err != nil {
		return "", err
	}
	req := larkim.NewCreateImageReqBuilder().
		Body(larkim.NewCreateImageReqBodyBuilder().
			ImageType(larkim.ImageTypeMessage).
			Image(reader).
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
