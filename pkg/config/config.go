package config

import "scutbot.cn/web/rm-monitor/pkg/redisx"

type PostgresConf struct {
	DSN         string
	AutoMigrate bool `json:",optional"`
}

type RedisConf = redisx.Conf

type PriorityItem struct {
	School   string `json:",optional"`
	Priority int    `json:",optional"`
}

type MonitorConf struct {
	ScheduleURL string `json:",optional"`
}

func (c *MonitorConf) WithDefaults() MonitorConf {
	out := *c
	if out.ScheduleURL == "" {
		out.ScheduleURL = "https://pro-robomasters-hz-n5i3.oss-cn-hangzhou.aliyuncs.com/live_json/schedule.json"
	}
	return out
}

type RecordConf struct {
	Res               string   `json:",optional"`
	LiveInfoURL       string   `json:",optional"`
	BaseDir           string   `json:",optional"`
	PathTemplate      string   `json:",optional"`
	MatchDirTemplate  string   `json:",optional"`
	MatchNameTemplate string   `json:",optional"`
	RoleBlackList     []string `json:",optional"`
}

func (c *RecordConf) WithDefaults() RecordConf {
	out := *c
	if out.Res == "" {
		out.Res = "middle"
	}
	if out.LiveInfoURL == "" {
		out.LiveInfoURL = "https://rm-static.djicdn.com/live_json/live_game_info.json"
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
	ImagePullPolicy          string `json:",optional"`
	BackoffLimit             int32  `json:",optional"`
	TTLSecondsAfterFinished  int32  `json:",optional"`
}

type TranscodeConf struct {
	BaseDir             string `json:",optional"`
	SourceRetentionDays int    `json:",optional"`
	MaxConcurrent       int    `json:",optional"`
	CPURequest          string `json:",optional"`
	CPULimit            string `json:",optional"`
	MemoryRequest       string `json:",optional"`
	MemoryLimit         string `json:",optional"`
	LocalWorkDir        string `json:",optional"`
}

func (c *TranscodeConf) WithDefaults() TranscodeConf {
	out := *c
	if out.BaseDir == "" {
		out.BaseDir = "/records"
	}
	if out.SourceRetentionDays <= 0 {
		out.SourceRetentionDays = 7
	}
	if out.MaxConcurrent <= 0 {
		out.MaxConcurrent = 3
	}
	if out.CPURequest == "" {
		out.CPURequest = "6000m"
	}
	if out.CPULimit == "" {
		out.CPULimit = "8000m"
	}
	if out.MemoryRequest == "" {
		out.MemoryRequest = "2Gi"
	}
	if out.MemoryLimit == "" {
		out.MemoryLimit = "6Gi"
	}
	if out.LocalWorkDir == "" {
		out.LocalWorkDir = "/tmp/rm-monitor-transcode"
	}
	return out
}

type LarkConf struct {
	AppId     string `json:",optional"`
	AppSecret string `json:",optional"`
}

type UploadConf struct {
	BaseDir            string `json:",optional"`
	BitableAppToken    string `json:",optional"`
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
		out.StorageNodeSelectorKey = "rm-monitor/record"
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
	if out.ImagePullPolicy == "" {
		out.ImagePullPolicy = "Always"
	}
	if out.BackoffLimit == 0 {
		out.BackoffLimit = 1
	}
	if out.TTLSecondsAfterFinished == 0 {
		out.TTLSecondsAfterFinished = 86400
	}
	return out
}
