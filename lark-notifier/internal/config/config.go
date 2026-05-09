package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	LarkConf struct {
		AppId     string
		AppSecret string
	}
	RecordConf struct {
		BaseDir  string
		RootNode string
	}
	UploadConf struct {
		Concurrency  int
		PartRetries  int
		RetryBackoff int
	}
	PostgresConf common.PostgresConf
	RedisConf    common.RedisConf
}
