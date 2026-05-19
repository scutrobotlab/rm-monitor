package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf     common.PostgresConf
	RedisConf        common.RedisConf
	RecordConf       common.RecordConf
	WhisperServerUrl string `json:",optional"`
}
