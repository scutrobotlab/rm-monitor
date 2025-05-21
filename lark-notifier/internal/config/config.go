package config

import (
	"github.com/zeromicro/go-queue/natsq"
	"github.com/zeromicro/go-zero/core/stores/redis"
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
	LarkConf struct {
		AppId     string
		AppSecret string
	}
	RecordConf struct {
		BaseDir  string
		RootNode string
	}
	NatsConf  NatsConf
	RedisConf       redis.RedisConf
}
