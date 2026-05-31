package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf common.PostgresConf
	RedisConf    common.RedisConf
	RecordConf   common.RecordConf
	DifyConf     common.DifyConf     `json:",optional"`
	ManifestConf common.ManifestConf `json:",optional"`
}
