{{- define "gopay-service.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "gopay-service.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "gopay-service.labels" -}}
app: {{ include "gopay-service.name" . }}
release: {{ .Release.Name }}
{{- end }}
