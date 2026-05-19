package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf common.PostgresConf
	RedisConf    common.RedisConf
	K8sJobConf   common.K8sJobConf `json:",optional"`
}
