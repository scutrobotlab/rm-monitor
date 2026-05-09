package larkcache

import (
	"context"
	"time"

	"scutbot.cn/web/rm-monitor/pkg/redisx"
)

type LarkCache struct {
	c *redisx.Client
}

func (l LarkCache) Set(ctx context.Context, key string, value string, expireTime time.Duration) error {
	return l.c.SetexCtx(ctx, key, value, int(expireTime.Seconds()))
}

func (l LarkCache) Get(ctx context.Context, key string) (string, error) {
	return l.c.GetCtx(ctx, key)
}

func NewLarkCache(c *redisx.Client) *LarkCache {
	return &LarkCache{
		c: c,
	}
}
