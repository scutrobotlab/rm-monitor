package config

import "github.com/zeromicro/go-queue/kq"

type Config struct {
	KqPusherConf struct {
		Brokers []string
		Topic   string
	}
	KqConsumerConf kq.KqConf
	RecordConf     struct {
		Res     string
		BaseDir string
	}
}
