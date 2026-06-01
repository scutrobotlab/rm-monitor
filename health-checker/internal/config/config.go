package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf common.PostgresConf
	RedisConf    common.RedisConf
	ArgoConf     common.ArgoConf   `json:",optional"`
	K8sJobConf   common.K8sJobConf `json:",optional"`
}
