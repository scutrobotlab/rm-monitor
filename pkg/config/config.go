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
	AudioRoles        []string `json:",optional"`
	STTRole           string   `json:",optional"`
}

type DanmuConf struct {
	Enabled bool   `json:",optional"`
	AppID   string `json:",optional"`
	AppKey  string `json:",optional"`
}

func (c *DanmuConf) WithDefaults() DanmuConf {
	return *c
}

type LLMConf struct {
	BaseURL        string `json:",optional"`
	APIKey         string `json:",optional"`
	Model          string `json:",optional"`
	TimeoutSeconds int    `json:",optional"`
}

type ReportConf = LLMConf

func (c *LLMConf) WithDefaults() LLMConf {
	out := *c
	if out.TimeoutSeconds <= 0 {
		out.TimeoutSeconds = 120
	}
	return out
}

type HighlightConf struct {
	Enabled               bool                   `json:",optional"`
	Role                  string                 `json:",optional"`
	AlgorithmVersion      string                 `json:",optional"`
	MaxHighlightsPerRound int                    `json:",optional"`
	MaxConcurrentJobs     int                    `json:",optional"`
	MinClipSeconds        int                    `json:",optional"`
	MaxClipSeconds        int                    `json:",optional"`
	PreSeconds            int                    `json:",optional"`
	PostSeconds           int                    `json:",optional"`
	MergeGapSeconds       int                    `json:",optional"`
	Publish               map[string]interface{} `json:",optional"`
}

func (c *HighlightConf) WithDefaults() HighlightConf {
	out := *c
	if out.Role == "" {
		out.Role = "主视角"
	}
	if out.AlgorithmVersion == "" {
		out.AlgorithmVersion = "danmu-zscore-v1"
	}
	if out.MaxHighlightsPerRound <= 0 {
		out.MaxHighlightsPerRound = 5
	}
	if out.MaxConcurrentJobs <= 0 {
		out.MaxConcurrentJobs = 3
	}
	if out.MinClipSeconds <= 0 {
		out.MinClipSeconds = 20
	}
	if out.MaxClipSeconds <= 0 {
		out.MaxClipSeconds = 90
	}
	if out.PreSeconds <= 0 {
		out.PreSeconds = 15
	}
	if out.PostSeconds <= 0 {
		out.PostSeconds = 25
	}
	if out.MergeGapSeconds <= 0 {
		out.MergeGapSeconds = 20
	}
	return out
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
	if len(out.AudioRoles) == 0 {
		out.AudioRoles = []string{"主视角"}
	}
	return out
}

type K8sJobConf struct {
	Namespace               string `json:",optional"`
	Image                   string
	ConfigMapName           string `json:",optional"`
	ServiceAccountName      string `json:",optional"`
	RecordsPVC              string `json:",optional"`
	RecordsMountPath        string `json:",optional"`
	ImagePullPolicy         string `json:",optional"`
	BackoffLimit            int32  `json:",optional"`
	TTLSecondsAfterFinished int32  `json:",optional"`
}

type TranscodeConf struct {
	BaseDir             string `json:",optional"`
	SourceRetentionDays int    `json:",optional"`
	MaxConcurrent       int    `json:",optional"`
	CPURequest          string `json:",optional"`
	CPULimit            string `json:",optional"`
	MemoryRequest       string `json:",optional"`
	MemoryLimit         string `json:",optional"`
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
