{{- define "tilegate.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "tilegate.fullname" -}}
{{- if contains .Chart.Name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "tilegate.selectorLabels" -}}
app.kubernetes.io/name: {{ include "tilegate.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "tilegate.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "tilegate.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "tilegate.secretName" -}}
{{- default (include "tilegate.fullname" .) .Values.auth.existingSecret }}
{{- end }}

{{- define "tilegate.configMapName" -}}
{{- default (include "tilegate.fullname" .) .Values.existingConfigMap }}
{{- end }}

{{/* Which database option is in use: secret, url, cnpg, postgres, sqlite or "". */}}
{{- define "tilegate.databaseKind" -}}
{{- if .Values.database.existingSecret }}secret
{{- else if .Values.database.url }}{{ if hasPrefix "sqlite" .Values.database.url }}sqlite{{ else }}url{{ end }}
{{- else if .Values.database.cnpg.enabled }}cnpg
{{- else if .Values.database.postgres.host }}postgres
{{- else if .Values.persistence.enabled }}sqlite
{{- end }}
{{- end }}

{{- define "tilegate.cnpgName" -}}
{{- printf "%s-db" (include "tilegate.fullname" .) | trunc 50 | trimSuffix "-" }}
{{- end }}
