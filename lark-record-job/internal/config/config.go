package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	LarkConf     common.LarkConf
	UploadConf   common.UploadConf
	RedisConf    common.RedisConf
	PostgresConf common.PostgresConf
}
