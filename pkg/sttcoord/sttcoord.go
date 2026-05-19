package sttcoord

import (
	"context"
	"fmt"
	"strconv"

	"scutbot.cn/web/rm-monitor/pkg/redisx"
)

const (
	StatusPending = "PENDING"
	StatusDone    = "DONE"
	StatusFailed  = "FAILED"

	ttlSeconds = 48 * 60 * 60
)

func Key(matchID string) string {
	return "rm-monitor:stt:match:" + matchID
}

func Field(roundNo int) string {
	return strconv.Itoa(roundNo)
}

func Set(ctx context.Context, redis *redisx.Client, matchID string, roundNo int, status string) error {
	if redis == nil {
		return nil
	}
	key := Key(matchID)
	if err := redis.HSetCtx(ctx, key, Field(roundNo), status); err != nil {
		return fmt.Errorf("set stt status: %w", err)
	}
	if err := redis.ExpireCtx(ctx, key, ttlSeconds); err != nil {
		return fmt.Errorf("expire stt status: %w", err)
	}
	return nil
}

func SetPending(ctx context.Context, redis *redisx.Client, matchID string, roundNo int) error {
	if redis == nil {
		return nil
	}
	key := Key(matchID)
	if _, err := redis.HSetNXCtx(ctx, key, Field(roundNo), StatusPending); err != nil {
		return fmt.Errorf("set pending stt status: %w", err)
	}
	if err := redis.ExpireCtx(ctx, key, ttlSeconds); err != nil {
		return fmt.Errorf("expire stt status: %w", err)
	}
	return nil
}

func GetMatch(ctx context.Context, redis *redisx.Client, matchID string) (map[string]string, error) {
	if redis == nil {
		return nil, nil
	}
	statuses, err := redis.HGetAllCtx(ctx, Key(matchID))
	if err != nil {
		return nil, fmt.Errorf("get stt status: %w", err)
	}
	return statuses, nil
}
