package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf      common.PostgresConf
	RedisConf         common.RedisConf
	RecordConf        common.RecordConf
	DanmuConf         common.DanmuConf `json:",optional"`
	K8sJobConf        common.K8sJobConf
	ManifestJobConf   common.K8sJobConf     `json:",optional"`
	STTJobConf        common.K8sJobConf     `json:",optional"`
	DanmuJobConf      common.K8sJobConf     `json:",optional"`
	WhisperServerUrls []string              `json:",optional"`
	Priority          []common.PriorityItem `json:",optional"`
}
