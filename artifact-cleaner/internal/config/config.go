package config

import "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf  config.PostgresConf
	TranscodeConf config.TranscodeConf
}
