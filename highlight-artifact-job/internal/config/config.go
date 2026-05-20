package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf  common.PostgresConf
	RecordConf    common.RecordConf
	HighlightConf common.HighlightConf
	LLMConf       common.LLMConf `json:",optional"`
}
