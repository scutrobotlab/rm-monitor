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
Always
{{- end -}}

{{- define "rm-monitor.jobImagePullPolicy" -}}
IfNotPresent
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
  {{- if .Values.stt.enabled }}
  STTRole: {{ .Values.stt.role | quote }}
  {{- end }}
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
