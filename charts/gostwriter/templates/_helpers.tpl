{{- define "gostwriter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "gostwriter.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := include "gostwriter.name" . -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "gostwriter.chart" -}}
{{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "gostwriter.labels" -}}
helm.sh/chart: {{ include "gostwriter.chart" . }}
app.kubernetes.io/name: {{ include "gostwriter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ default .Chart.AppVersion .Values.image.tag | quote }}
{{- end -}}

{{- define "gostwriter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gostwriter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "gostwriter.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- if .Values.serviceAccount.name -}}
{{- .Values.serviceAccount.name -}}
{{- else -}}
{{- include "gostwriter.fullname" . -}}
{{- end -}}
{{- else -}}
default
{{- end -}}
{{- end -}}

{{- define "gostwriter.secretEnvName" -}}
{{- if .Values.existingSecret -}}
{{- .Values.existingSecret -}}
{{- else if .Values.secretEnv.create -}}
{{- if .Values.secretEnv.name -}}
{{- .Values.secretEnv.name -}}
{{- else -}}
{{- printf "%s-env" (include "gostwriter.fullname" .) -}}
{{- end -}}
{{- end -}}
{{- end -}}