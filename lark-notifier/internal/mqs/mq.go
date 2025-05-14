package mqs

import (
	"context"

	"github.com/pkg/errors"
	"github.com/zeromicro/go-zero/core/jsonx"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/monitor/types"

	"github.com/zeromicro/go-queue/kq"
	"github.com/zeromicro/go-zero/core/service"
)

type consumerRouter struct {
	consumerMuxes []consumerMux
}

func (c consumerRouter) Consume(ctx context.Context, key, value string) error {
	for _, mux := range c.consumerMuxes {
		if mux.filter(key) {
			return mux.handler(ctx, key, value)
		}
	}
	return nil
}

func NewConsumerRouter(svcContext *svc.ServiceContext) []service.Service {
	return []service.Service{
		kq.MustNewQueue(svcContext.Config.MonitorConsumer, consumerRouter{
			consumerMuxes: []consumerMux{
				generalMux(svcContext, types.IsMatchStart, NewMatchStartLogic),
				generalMux(svcContext, types.IsMatchNewRound, NewMatchNewRoundLogic),
				generalMux(svcContext, types.IsMatchDone, NewMatchDoneLogic),
			},
		}),
		kq.MustNewQueue(svcContext.Config.RecordConsumer, consumerRouter{
			consumerMuxes: []consumerMux{
				generalMuxNoFilter(svcContext, NewRecordCompletedLogic),
			},
		}),
	}
}

type consumerMux struct {
	filter  func(string) bool
	handler func(ctx context.Context, key, value string) error
}

type Consumer[T any] interface {
	Consume(key string, m T) error
}

func generalMux[T any](svcCtx *svc.ServiceContext, filter func(string) bool, newConsumerFunc func(ctx context.Context, svcCtx *svc.ServiceContext) Consumer[T]) consumerMux {
	return consumerMux{
		filter: filter,
		handler: func(ctx context.Context, key, value string) error {
			var m T
			if err := jsonx.Unmarshal([]byte(value), &m); err != nil {
				return errors.Wrapf(err, "failed to unmarshal %s", value)
			}

			l := newConsumerFunc(ctx, svcCtx)
			return l.Consume(key, m)
		},
	}
}

func generalMuxNoFilter[T any](svcCtx *svc.ServiceContext, newConsumerFunc func(ctx context.Context, svcCtx *svc.ServiceContext) Consumer[T]) consumerMux {
	return consumerMux{
		filter: func(key string) bool { return true },
		handler: func(ctx context.Context, key, value string) error {
			var m T
			if err := jsonx.Unmarshal([]byte(value), &m); err != nil {
				return errors.Wrapf(err, "failed to unmarshal %s", value)
			}

			l := newConsumerFunc(ctx, svcCtx)
			return l.Consume(key, m)
		},
	}
}
