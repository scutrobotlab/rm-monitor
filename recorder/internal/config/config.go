package config

import (
	"github.com/zeromicro/go-queue/natsq"
)

type NatsConf struct {
	ServerUri  string
	ClientName string
}

func (n *NatsConf) Conf() *natsq.NatsConfig {
	return &natsq.NatsConfig{
		ServerUri:  n.ServerUri,
		ClientName: n.ClientName,
	}
}

type Config struct {
	NatsConf   NatsConf
	RecordConf struct {
		Res     string
		BaseDir string
	}
}
