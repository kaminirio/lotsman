{{- define "lotsman-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "lotsman-agent.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "lotsman-agent.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "lotsman-agent.labels" -}}
helm.sh/chart: {{ include "lotsman-agent.chart" . }}
{{ include "lotsman-agent.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: lotsman
{{- end }}

{{- define "lotsman-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "lotsman-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: agent
{{- end }}

{{- define "lotsman-agent.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "lotsman-agent.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "lotsman-agent.agentTokenSecret" -}}
{{- if .Values.agentToken.existingSecret }}
{{- .Values.agentToken.existingSecret }}
{{- else }}
{{- printf "%s-agent-token" (include "lotsman-agent.fullname" .) }}
{{- end }}
{{- end }}

{{- define "lotsman-agent.agentTokenSecretKey" -}}
{{- if .Values.agentToken.existingSecret }}
{{- .Values.agentToken.existingSecretKey }}
{{- else }}
{{- "agent-token" }}
{{- end }}
{{- end }}

{{- define "lotsman-agent.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}
