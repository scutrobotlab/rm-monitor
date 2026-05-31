package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	RecordConf        common.RecordConf
	DifyConf          common.DifyConf       `json:",optional"`
	STTQualityConf    common.STTQualityConf `json:",optional"`
	WhisperServerUrls []string              `json:",optional"`
}
