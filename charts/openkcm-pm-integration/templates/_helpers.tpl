{{- define "openkcm-pm-integration.labels" -}}
app.kubernetes.io/name: openkcm-pm-integration
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: Helm
app.kubernetes.io/part-of: openkcm
app.kubernetes.io/version: {{ .Chart.AppVersion }}
{{- end }}
