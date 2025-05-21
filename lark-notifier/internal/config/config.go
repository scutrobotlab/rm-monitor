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
	RecordConf struct {
		BaseDir  string
		RootNode string
	}
	MonitorConsumer kq.KqConf
	RecordConsumer  kq.KqConf
	RedisConf       redis.RedisConf
}
