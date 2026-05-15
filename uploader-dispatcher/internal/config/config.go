package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	PostgresConf common.PostgresConf
	RedisConf    common.RedisConf
	LarkConf     common.LarkConf
	UploadConf   common.UploadConf
	K8sJobConf   common.K8sJobConf
	Priority     []common.PriorityItem `json:",optional"`
}
