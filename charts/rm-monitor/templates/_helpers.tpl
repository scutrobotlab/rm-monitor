{{- define "rm-monitor.namespace" -}}
{{- .Release.Namespace -}}
{{- end -}}

{{- define "rm-monitor.image" -}}
{{- $appVersion := .root.Chart.AppVersion | default "dev" -}}
{{- $tag := "dev" -}}
{{- if eq $appVersion "dev" -}}
{{- $tag = "dev" -}}
{{- else if hasPrefix "sha-" $appVersion -}}
{{- $tag = $appVersion -}}
{{- else -}}
{{- $tag = printf "sha-%s" ($appVersion | trunc 7) -}}
{{- end -}}
{{- printf "ghcr.io/scutrobotlab/rm-monitor/%s:%s" .component $tag -}}
{{- end -}}

{{- define "rm-monitor.imagePullPolicy" -}}
{{ .Values.imagePullPolicy | default "Always" }}
{{- end -}}

{{- define "rm-monitor.jobImagePullPolicy" -}}
{{ .Values.jobImagePullPolicy | default "IfNotPresent" }}
{{- end -}}

{{- define "rm-monitor.configPath" -}}
/app/etc/config.yml
{{- end -}}

{{- define "rm-monitor.commonLabels" -}}
app.kubernetes.io/instance: {{ .Release.Name | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end -}}

{{- define "rm-monitor.selectorLabels" -}}
app.kubernetes.io/name: {{ .name | quote }}
{{- end -}}

{{- define "rm-monitor.postgresConf" -}}
PostgresConf:
  DSN: {{ .Values.postgres.dsn }}
{{- end -}}

{{- define "rm-monitor.redisConf" -}}
RedisConf:
  Host: {{ .Values.redis.host }}
  Pass: {{ .Values.redis.pass | quote }}
  Type: {{ .Values.redis.type }}
{{- end -}}

{{- define "rm-monitor.recordConf" -}}
RecordConf:
  Res: {{ .Values.record.res }}
  BaseDir: {{ .Values.record.baseDir }}
  {{- if .Values.record.liveInfoURL }}
  LiveInfoURL: {{ .Values.record.liveInfoURL }}
  {{- end }}
  AudioRoles:
{{ toYaml .Values.record.audioRoles | indent 4 }}
  {{- if .Values.stt.enabled }}
  STTRole: {{ .Values.stt.role | quote }}
  {{- end }}
{{- end -}}

{{- define "rm-monitor.danmuConf" -}}
DanmuConf:
  Enabled: {{ .Values.danmu.enabled }}
  AppID: {{ .Values.danmu.appId | quote }}
  AppKey: {{ .Values.danmu.appKey | quote }}
  VideoOffsetSeconds: {{ .Values.danmu.videoOffsetSeconds }}
{{- end -}}

{{- define "rm-monitor.ocrConf" -}}
OCRConf:
  Enabled: {{ .Values.ocr.enabled }}
  Role: {{ .Values.ocr.role | quote }}
  FrameInterval: {{ .Values.ocr.frameInterval }}
  SimilarityThreshold: {{ .Values.ocr.similarityThreshold }}
  MaxConcurrentJobs: {{ .Values.ocr.maxConcurrentJobs }}
{{- end -}}

{{- define "rm-monitor.highlightConf" -}}
HighlightConf:
  Enabled: {{ .Values.highlight.enabled }}
  Role: {{ .Values.highlight.role | quote }}
  AlgorithmVersion: {{ .Values.highlight.algorithmVersion | quote }}
  ReviewWorkflowAPIKey: {{ .Values.highlight.reviewWorkflowAPIKey | quote }}
  MaxHighlightsPerRound: {{ .Values.highlight.maxHighlightsPerRound }}
  MaxConcurrentJobs: {{ .Values.highlight.maxConcurrentJobs }}
  MinClipSeconds: {{ .Values.highlight.minClipSeconds }}
  MaxClipSeconds: {{ .Values.highlight.maxClipSeconds }}
  PreSeconds: {{ .Values.highlight.preSeconds }}
  PostSeconds: {{ .Values.highlight.postSeconds }}
  MergeGapSeconds: {{ .Values.highlight.mergeGapSeconds }}
  Publish:
{{ toYaml .Values.highlight.publish | indent 4 }}
{{- end -}}

{{- define "rm-monitor.publishConf" -}}
PublishConf:
  Bilibili:
    Enabled: {{ .Values.publish.bilibili.enabled }}
    CookieSecretName: {{ .Values.publish.bilibili.cookieSecretName | quote }}
    CookieSecretKey: {{ .Values.publish.bilibili.cookieSecretKey | quote }}
    CookiePath: "/etc/biliup/{{ .Values.publish.bilibili.cookieSecretKey }}"
    TID: {{ .Values.publish.bilibili.tid }}
    Copyright: {{ .Values.publish.bilibili.copyright }}
    Source: {{ .Values.publish.bilibili.source | quote }}
    TitleTemplate: {{ .Values.publish.bilibili.titleTemplate | quote }}
    DescTemplate: |
{{ .Values.publish.bilibili.descTemplate | indent 6 }}
    DynamicTemplate: {{ .Values.publish.bilibili.dynamicTemplate | quote }}
    Tags:
{{ toYaml .Values.publish.bilibili.tags | indent 6 }}
    NoReprint: {{ .Values.publish.bilibili.noReprint }}
    OpenElec: {{ .Values.publish.bilibili.openElec }}
    MaxConcurrentJobs: {{ .Values.publish.bilibili.maxConcurrentJobs }}
    Cover:
      Enabled: {{ .Values.publish.bilibili.cover.enabled }}
{{- end -}}

{{- define "rm-monitor.difyConf" -}}
DifyConf:
  BaseURL: {{ .Values.dify.baseURL }}
  TimeoutSeconds: {{ .Values.dify.timeoutSeconds }}
{{- end -}}

{{- define "rm-monitor.priority" -}}
Priority:
{{- if .Values.priority }}
{{ toYaml .Values.priority | indent 2 }}
{{- else }}
  []
{{- end }}
{{- end -}}

{{- define "rm-monitor.jobConf" -}}
Namespace: {{ include "rm-monitor.namespace" .root }}
Image: {{ include "rm-monitor.image" (dict "root" .root "component" .component) }}
ConfigMapName: {{ .configMapName }}
ServiceAccountName: {{ .root.Values.serviceAccount.name }}
RecordsPVC: {{ .recordsPVC }}
RecordsMountPath: {{ .root.Values.jobs.recordsMountPath }}
ImagePullPolicy: {{ include "rm-monitor.jobImagePullPolicy" .root }}
BackoffLimit: {{ .root.Values.jobs.backoffLimit }}
TTLSecondsAfterFinished: {{ .root.Values.jobs.ttlSecondsAfterFinished }}
{{- end -}}
