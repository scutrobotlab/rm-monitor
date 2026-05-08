package config

import (
	"github.com/zeromicro/go-zero/core/stores/redis"
	common "scutbot.cn/web/rm-monitor/pkg/config"
)

type Config struct {
	PostgresConf common.PostgresConf
	LarkConf     common.LarkConf
	UploadConf   common.UploadConf
	RedisConf    redis.RedisConf
}
