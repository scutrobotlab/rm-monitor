package larkrate

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/pkg/redisx"
)

type Limiter struct {
	redis *redisx.Client
}

func New(redis *redisx.Client) *Limiter {
	return &Limiter{redis: redis}
}

func (l *Limiter) Wait(ctx context.Context, chatID string) error {
	for {
		now := time.Now()
		if ok, wait, err := l.allow(ctx, fmt.Sprintf("rm-monitor:lark-rate:global:sec:%d", now.Unix()), 50, 2); err != nil {
			return err
		} else if !ok {
			if err := sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		if ok, wait, err := l.allow(ctx, fmt.Sprintf("rm-monitor:lark-rate:global:min:%s", now.Format("200601021504")), 1000, 90); err != nil {
			return err
		} else if !ok {
			if err := sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		if chatID != "" {
			if ok, wait, err := l.allow(ctx, fmt.Sprintf("rm-monitor:lark-rate:chat:%s:%d", chatID, now.Unix()), 5, 2); err != nil {
				return err
			} else if !ok {
				if err := sleep(ctx, wait); err != nil {
					return err
				}
				continue
			}
		}
		return nil
	}
}

func (l *Limiter) allow(ctx context.Context, key string, limit int64, ttlSeconds int) (bool, time.Duration, error) {
	n, err := l.redis.IncrCtx(ctx, key)
	if err != nil {
		return false, 0, errors.Wrap(err, "increment lark rate limit")
	}
	if n == 1 {
		_ = l.redis.ExpireCtx(ctx, key, ttlSeconds)
	}
	if n <= limit {
		return true, 0, nil
	}
	if ttlSeconds > 2 {
		return false, time.Until(time.Now().Truncate(time.Minute).Add(time.Minute)), nil
	}
	return false, time.Until(time.Now().Truncate(time.Second).Add(time.Second)), nil
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
