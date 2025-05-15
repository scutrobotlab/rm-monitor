package config

import (
	"github.com/zeromicro/go-queue/natsq"
)

type Config struct {
	NatsConf   natsq.NatsConfig
	RecordConf struct {
		Res     string
		BaseDir string
	}
}
