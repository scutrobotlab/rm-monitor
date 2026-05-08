package config

import "github.com/zeromicro/go-zero/core/stores/redis"

type PostgresConf struct {
	DSN         string
	AutoMigrate bool
}

type RedisConf = redis.RedisConf

type RecordConf struct {
	Res               string
	BaseDir           string
	PathTemplate      string
	MatchDirTemplate  string
	MatchNameTemplate string
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
	Namespace                string
	Image                    string
	ConfigMapName            string
	ServiceAccountName       string
	StorageNodeSelectorKey   string
	StorageNodeSelectorValue string
	RecordsPVC               string
	RecordsMountPath         string
	BackoffLimit             int32
	TTLSecondsAfterFinished  int32
}

type TranscodeConf struct {
	BaseDir                    string
	AllowedWindow              string
	SuspendWhenRecordingActive bool
	SourceRetentionDays        int
	CPURequest                 string
	CPULimit                   string
	MemoryRequest              string
	MemoryLimit                string
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
	AppId     string
	AppSecret string
}

type UploadConf struct {
	BaseDir            string
	RootNode           string
	Concurrency        int
	PartRetries        int
	RetryBackoff       int
	RateLimitKey       string
	RateLimitPerMinute int
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
