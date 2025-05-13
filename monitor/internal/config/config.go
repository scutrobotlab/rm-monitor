package config

import "github.com/zeromicro/go-zero/core/stores/redis"

type Config struct {
	KqPusherConf struct {
		Brokers []string
		Topic   string
	}
	RedisConf redis.RedisConf
}
