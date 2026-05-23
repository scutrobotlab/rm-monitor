package config

import common "scutbot.cn/web/rm-monitor/pkg/config"

type Config struct {
	RecordConf    common.RecordConf
	PythonBin     string `json:",optional"`
	ScriptDir     string `json:",optional"`
	CondEnvDir    string `json:",optional"`
	FrameInterval int    `json:",optional"`
	TemplatePath  string `json:",optional"`
	SimilarityThr float64 `json:",optional"`
}
