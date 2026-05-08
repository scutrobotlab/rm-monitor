package config

import "github.com/zeromicro/go-zero/core/stores/redis"

type PostgresConf struct {
	DSN         string
	AutoMigrate bool `json:",optional"`
}

type RedisConf = redis.RedisConf

type RecordConf struct {
	Res               string `json:",optional"`
	BaseDir           string `json:",optional"`
	PathTemplate      string `json:",optional"`
	MatchDirTemplate  string `json:",optional"`
	MatchNameTemplate string `json:",optional"`
}

func (c *RecordConf) WithDefaults() RecordConf {
	out := *c
	if out.Res == "" {
		out.Res = "middle"
	}
	if out.BaseDir == "" {
		out.BaseDir = "/records"
	}
	if out.PathTemplate == "" {
		out.PathTemplate = "{{.Event}}/{{.Zone}}/{{.MatchName}}/Round-{{.RoundNo}}/{{.Role}}.flv"
	}
	if out.MatchDirTemplate == "" {
		out.MatchDirTemplate = "{{.Event}}/{{.Zone}}/{{.MatchName}}"
	}
	if out.MatchNameTemplate == "" {
		out.MatchNameTemplate = "{{.Order}}. {{.RedSchool}}-{{.RedName}} VS {{.BlueSchool}}-{{.BlueName}}"
	}
	return out
}

type K8sJobConf struct {
	Namespace                string `json:",optional"`
	Image                    string
	ConfigMapName            string `json:",optional"`
	ServiceAccountName       string `json:",optional"`
	StorageNodeSelectorKey   string `json:",optional"`
	StorageNodeSelectorValue string `json:",optional"`
	RecordsPVC               string `json:",optional"`
	RecordsMountPath         string `json:",optional"`
	BackoffLimit             int32  `json:",optional"`
	TTLSecondsAfterFinished  int32  `json:",optional"`
}

type TranscodeConf struct {
	BaseDir                    string `json:",optional"`
	AllowedWindow              string `json:",optional"`
	SuspendWhenRecordingActive bool   `json:",optional"`
	SourceRetentionDays        int    `json:",optional"`
	CPURequest                 string `json:",optional"`
	CPULimit                   string `json:",optional"`
	MemoryRequest              string `json:",optional"`
	MemoryLimit                string `json:",optional"`
}

func (c *TranscodeConf) WithDefaults() TranscodeConf {
	out := *c
	if out.BaseDir == "" {
		out.BaseDir = "/records"
	}
	if out.AllowedWindow == "" {
		out.AllowedWindow = "23:00-06:00"
	}
	if out.SourceRetentionDays <= 0 {
		out.SourceRetentionDays = 7
	}
	if out.CPURequest == "" {
		out.CPURequest = "500m"
	}
	if out.MemoryRequest == "" {
		out.MemoryRequest = "512Mi"
	}
	if out.MemoryLimit == "" {
		out.MemoryLimit = "2Gi"
	}
	return out
}

type LarkConf struct {
	AppId     string `json:",optional"`
	AppSecret string `json:",optional"`
}

type UploadConf struct {
	BaseDir            string `json:",optional"`
	RootNode           string `json:",optional"`
	Concurrency        int    `json:",optional"`
	PartRetries        int    `json:",optional"`
	RetryBackoff       int    `json:",optional"`
	RateLimitKey       string `json:",optional"`
	RateLimitPerMinute int    `json:",optional"`
}

func (c *UploadConf) WithDefaults() UploadConf {
	out := *c
	if out.BaseDir == "" {
		out.BaseDir = "/records"
	}
	if out.Concurrency <= 0 {
		out.Concurrency = 1
	}
	if out.PartRetries <= 0 {
		out.PartRetries = 3
	}
	if out.RetryBackoff <= 0 {
		out.RetryBackoff = 2
	}
	if out.RateLimitKey == "" {
		out.RateLimitKey = "rm-monitor:lark-upload"
	}
	if out.RateLimitPerMinute <= 0 {
		out.RateLimitPerMinute = 30
	}
	return out
}

func (c *K8sJobConf) WithDefaults() K8sJobConf {
	out := *c
	if out.Namespace == "" {
		out.Namespace = "rm-monitor"
	}
	if out.StorageNodeSelectorKey == "" {
		out.StorageNodeSelectorKey = "rm-monitor/storage"
	}
	if out.StorageNodeSelectorValue == "" {
		out.StorageNodeSelectorValue = "true"
	}
	if out.RecordsPVC == "" {
		out.RecordsPVC = "rm-monitor-records"
	}
	if out.RecordsMountPath == "" {
		out.RecordsMountPath = "/records"
	}
	if out.BackoffLimit == 0 {
		out.BackoffLimit = 1
	}
	if out.TTLSecondsAfterFinished == 0 {
		out.TTLSecondsAfterFinished = 86400
	}
	return out
}
