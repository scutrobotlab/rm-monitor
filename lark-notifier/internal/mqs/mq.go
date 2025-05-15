package mqs

import (
	"context"

	"github.com/zeromicro/go-queue/natsq"

	"github.com/pkg/errors"
	"github.com/zeromicro/go-zero/core/jsonx"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/svc"
	"scutbot.cn/web/rm-monitor/monitor/types"

	"github.com/zeromicro/go-zero/core/service"
)

func NewConsumerRouter(svcContext *svc.ServiceContext) service.Service {
	return natsq.MustNewConsumerManager(svcContext.Config.NatsConf.Conf(), []*natsq.ConsumerQueue{
		natsqMux(svcContext, []string{types.MatchStartSubject}, "matchstart", NewMatchStartLogic),
		natsqMux(svcContext, []string{types.MatchNewRoundSubject}, "matchnewround", NewMatchNewRoundLogic),
		natsqMux(svcContext, []string{types.MatchDoneSubject}, "matchdone", NewMatchDoneLogic),
	}, natsq.NatDefaultMode)
}

type Consumer[T any] interface {
	Consume(key string, m T) error
}

type natsqConsumer struct {
	handler func(msg *natsq.Msg) error
}

func (n natsqConsumer) HandleMessage(m *natsq.Msg) error {
	return n.handler(m)
}

func natsqMux[T any](svcCtx *svc.ServiceContext, subjects []string, queue string, newConsumerFunc func(ctx context.Context, svcCtx *svc.ServiceContext) Consumer[T]) *natsq.ConsumerQueue {
	return &natsq.ConsumerQueue{
		QueueName: queue,
		Subjects:  subjects,
		Consumer: &natsqConsumer{
			func(msg *natsq.Msg) error {
				var v T
				if err := jsonx.Unmarshal(msg.Data, &v); err != nil {
					return errors.Wrap(err, "unmarshal error")
				}

				c := newConsumerFunc(context.Background(), svcCtx)
				return c.Consume(msg.Subject, v)
			},
		},
	}
}
