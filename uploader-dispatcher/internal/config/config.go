package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf common.PostgresConf
	UploadConf   common.UploadConf
	K8sJobConf   common.K8sJobConf
}
