package larkcache

import (
	"context"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"time"
)

type LarkCache struct {
	c *redis.Redis
}

func (l LarkCache) Set(ctx context.Context, key string, value string, expireTime time.Duration) error {
	return l.c.SetexCtx(ctx, key, value, int(expireTime.Seconds()))
}

func (l LarkCache) Get(ctx context.Context, key string) (string, error) {
	return l.c.GetCtx(ctx, key)
}

func NewLarkCache(c *redis.Redis) *LarkCache {
	return &LarkCache{
		c: c,
	}
}
