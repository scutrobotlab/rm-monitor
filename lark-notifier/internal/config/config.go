package config

import (
	"github.com/zeromicro/go-queue/natsq"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

type Config struct {
	LarkConf struct {
		AppId     string
		AppSecret string
	}
	NatsConf  natsq.NatsConfig
	RedisConf redis.RedisConf
}
