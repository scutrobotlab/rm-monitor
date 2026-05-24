package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf common.PostgresConf
	OCRConf      common.OCRConf
	K8sJobConf   common.K8sJobConf
	Priority     []common.PriorityItem `json:",optional"`
}
