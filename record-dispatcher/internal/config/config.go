package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf    common.PostgresConf
	RedisConf       common.RedisConf
	RecordConf      common.RecordConf
	K8sJobConf      common.K8sJobConf
	ManifestJobConf common.K8sJobConf     `json:",optional"`
	STTJobConf      common.K8sJobConf     `json:",optional"`
	Priority        []common.PriorityItem `json:",optional"`
}
