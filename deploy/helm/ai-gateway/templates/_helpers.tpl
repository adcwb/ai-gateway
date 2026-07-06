{{- define "ai-gateway.name" -}}
{{- .Chart.Name -}}
{{- end -}}
{{- define "ai-gateway.labels" -}}
app.kubernetes.io/name: {{ include "ai-gateway.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
{{- end -}}
{{- define "ai-gateway.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ai-gateway.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
