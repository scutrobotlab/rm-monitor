package config

type Config struct {
	KqPusherConf struct {
		Brokers []string
		Topic   string
	}
}
