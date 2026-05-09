package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf common.PostgresConf
	LarkConf     common.LarkConf
	UploadConf   common.UploadConf
	RedisConf    common.RedisConf
}
