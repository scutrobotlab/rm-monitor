package config

import (
	"github.com/zeromicro/go-queue/kq"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

type Config struct {
	LarkConf struct {
		AppId     string
		AppSecret string
	}
	KqConsumerConf kq.KqConf
	RedisConf      redis.RedisConf
}
