{{- define "rm-monitor.namespace" -}}
{{- .Release.Namespace -}}
{{- end -}}

{{- define "rm-monitor.image" -}}
{{- printf "%s/%s/%s:%s" .Values.image.registry .Values.image.repository .component .Values.image.tag -}}
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
  {{- if .Values.stt.enabled }}
  STTRole: {{ .Values.stt.role | quote }}
  {{- end }}
  PathTemplate: {{ .Values.record.pathTemplate | quote }}
  MatchDirTemplate: {{ .Values.record.matchDirTemplate | quote }}
  MatchNameTemplate: {{ .Values.record.matchNameTemplate | quote }}
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
Image: {{ include "rm-monitor.image" (dict "Values" .root.Values "component" .component) }}
ConfigMapName: {{ .configMapName }}
ServiceAccountName: {{ .root.Values.serviceAccount.name }}
RecordsPVC: {{ .recordsPVC }}
RecordsMountPath: {{ .root.Values.jobs.recordsMountPath }}
ImagePullPolicy: {{ default .root.Values.image.jobPullPolicy .pullPolicy }}
BackoffLimit: {{ .root.Values.jobs.backoffLimit }}
TTLSecondsAfterFinished: {{ .root.Values.jobs.ttlSecondsAfterFinished }}
{{- end -}}
