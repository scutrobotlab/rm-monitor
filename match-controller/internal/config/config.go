package config

import (
	common "scutbot.cn/web/rm-monitor/pkg/config"
)

type Config struct {
	MonitorConf       common.MonitorConf
	ArgoConf          common.ArgoConf `json:",optional"`
	PostgresConf      common.PostgresConf
	RedisConf         common.RedisConf
	Priority          []common.PriorityItem `json:",optional"`
	RecordConf        common.RecordConf     `json:",optional"`
	DanmuConf         common.DanmuConf      `json:",optional"`
	AnalyzeConf       common.AnalyzeConf    `json:",optional"`
	UploadConf        common.UploadConf     `json:",optional"`
	HighlightConf     common.HighlightConf  `json:",optional"`
	WhisperServerUrls []string              `json:",optional"`
}
