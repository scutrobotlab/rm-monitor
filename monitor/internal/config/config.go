package config

import (
	common "scutbot.cn/web/rm-monitor/pkg/config"
)

type Config struct {
	MonitorConf  common.MonitorConf
	PostgresConf common.PostgresConf
	RedisConf    common.RedisConf
}
