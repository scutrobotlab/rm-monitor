{{- define "rm-monitor.namespace" -}}
{{- .Release.Namespace -}}
{{- end -}}

{{- define "rm-monitor.image" -}}
{{- $appVersion := .root.Chart.AppVersion | default "dev" -}}
{{- $tag := "dev" -}}
{{- if .root.Values.imageTagOverride -}}
{{- $tag = .root.Values.imageTagOverride -}}
{{- else if eq $appVersion "dev" -}}
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

{{- define "rm-monitor.argoConf" -}}
ArgoConf:
  Enabled: {{ .Values.argo.enabled }}
  Namespace: {{ .Values.argo.namespace | default (include "rm-monitor.namespace" .) }}
  MatchWorkflowTemplate: {{ .Values.argo.matchWorkflowTemplate | quote }}
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
  RoleBlackList:
{{ toYaml .Values.record.roleBlackList | indent 4 }}
  StopDelaySeconds: {{ .Values.record.stopDelaySeconds }}
  {{- if .Values.stt.enabled }}
  STTRole: {{ .Values.stt.role | quote }}
  {{- end }}
{{- end -}}

{{- define "rm-monitor.analyzeConf" -}}
AnalyzeConf:
  Enabled: {{ .Values.analyze.enabled }}
  Role: {{ .Values.analyze.role | quote }}
  MaxConcurrentJobs: {{ .Values.analyze.maxConcurrentJobs }}
  Scan:
    FPS: {{ .Values.analyze.scan.fps }}
    Width: {{ .Values.analyze.scan.width }}
    Height: {{ .Values.analyze.scan.height }}
    MaxStartScanSeconds: {{ .Values.analyze.scan.maxStartScanSeconds }}
    MaxSettlementScanSeconds: {{ .Values.analyze.scan.maxSettlementScanSeconds }}
    SettlementChunkSeconds: {{ .Values.analyze.scan.settlementChunkSeconds }}
    MinSettlementSecond: {{ .Values.analyze.scan.minSettlementSecond }}
    MinRoundSeconds: {{ .Values.analyze.scan.minRoundSeconds }}
    SettlementTailSeconds: {{ .Values.analyze.scan.settlementTailSeconds }}
    SettlementRefineWindowSeconds: {{ .Values.analyze.scan.settlementRefineWindowSeconds }}
    SettlementRefineFPS: {{ .Values.analyze.scan.settlementRefineFPS }}
{{- end -}}

{{- define "rm-monitor.ocrServerConf" -}}
OCRServerConf:
  BaseURL: {{ .Values.ocrServer.baseURL | quote }}
  TimeoutSeconds: {{ .Values.ocrServer.timeoutSeconds | default 30 }}
{{- end -}}

{{- define "rm-monitor.danmuConf" -}}
DanmuConf:
  Enabled: {{ .Values.danmu.enabled }}
  AppID: {{ .Values.danmu.appId | quote }}
  AppKey: {{ .Values.danmu.appKey | quote }}
  VideoOffsetSeconds: {{ .Values.danmu.videoOffsetSeconds }}
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
  PreviewSeconds: {{ .Values.highlight.previewSeconds }}
  PreviewFPS: {{ .Values.highlight.previewFPS }}
  PreviewWidth: {{ .Values.highlight.previewWidth }}
  Publish:
{{ toYaml .Values.highlight.publish | indent 4 }}
{{- end -}}

{{- define "rm-monitor.difyConf" -}}
DifyConf:
  BaseURL: {{ .Values.dify.baseURL | quote }}
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
