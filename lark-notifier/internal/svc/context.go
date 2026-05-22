package svc

import (
	"context"
	"math/rand"
	"os"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/ent"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/config"
	"scutbot.cn/web/rm-monitor/pkg/db"
	"scutbot.cn/web/rm-monitor/pkg/larkcache"
	"scutbot.cn/web/rm-monitor/pkg/larklog"
	"scutbot.cn/web/rm-monitor/pkg/larkrate"
	"scutbot.cn/web/rm-monitor/pkg/logx"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
)

type ServiceContext struct {
	Config      config.Config
	LarkClient  *lark.Client
	RedisClient *redisx.Client
	RestyClient *resty.Client
	DB          *ent.Client
	RateLimiter *larkrate.Limiter
	UploadSlots chan struct{}
}

func NewServiceContext(c config.Config) *ServiceContext {
	restyClient := resty.New().SetRetryCount(3).SetRetryWaitTime(1 * time.Second).SetTimeout(10 * time.Second)
	redisClient := redisx.MustNew(c.RedisConf.WithDefaults())
	client, err := db.Open(context.Background(), c.PostgresConf)
	if err != nil {
		logx.Error(err)
		os.Exit(1)
	}
	uploadConcurrency := c.UploadConf.Concurrency
	if uploadConcurrency <= 0 {
		uploadConcurrency = 1
	}
	return &ServiceContext{
		Config: c,
		LarkClient: lark.NewClient(c.LarkConf.AppId, c.LarkConf.AppSecret,
			lark.WithHttpClient(restyClient.Client()),
			lark.WithEnableTokenCache(true),
			lark.WithTokenCache(larkcache.NewLarkCache(redisClient)),
			lark.WithLogger(larklog.NewLarkLog())),
		RedisClient: redisClient,
		RestyClient: restyClient,
		DB:          client,
		RateLimiter: larkrate.New(redisClient),
		UploadSlots: make(chan struct{}, uploadConcurrency),
	}
}

func (s *ServiceContext) RetryLark(ctx context.Context, chatID string, f func() error) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if err := s.RateLimiter.Wait(ctx, chatID); err != nil {
			return err
		}
		if err := f(); err != nil {
			last = err
			if attempt < 2 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(larkRetryDelay(attempt)):
				}
			}
			continue
		}
		return nil
	}
	return last
}

func larkRetryDelay(attempt int) time.Duration {
	base := time.Duration(1<<attempt) * time.Second
	jitter := time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
	return base + jitter
}
