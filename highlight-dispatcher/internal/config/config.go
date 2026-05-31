package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf    common.PostgresConf
	RecordConf      common.RecordConf
	DifyConf        common.DifyConf `json:",optional"`
	HighlightConf   common.HighlightConf
	PublishConf     common.PublishConf `json:",optional"`
	K8sJobConf      common.K8sJobConf
	BilibiliJobConf common.K8sJobConf `json:",optional"`
}
