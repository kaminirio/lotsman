{{/*
Expand the name of the chart.
*/}}
{{- define "lotsman.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name.
*/}}
{{- define "lotsman.fullname" -}}
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

{{- define "lotsman.controlPlane.fullname" -}}
{{- printf "%s-control-plane" (include "lotsman.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "lotsman.agent.fullname" -}}
{{- printf "%s-agent" (include "lotsman.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "lotsman.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "lotsman.labels" -}}
helm.sh/chart: {{ include "lotsman.chart" . }}
{{ include "lotsman.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "lotsman.selectorLabels" -}}
app.kubernetes.io/name: {{ include "lotsman.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Per-component labels (adds app.kubernetes.io/component).
Call as: (include "lotsman.componentLabels" (dict "root" . "component" "control-plane"))
*/}}
{{- define "lotsman.componentLabels" -}}
{{ include "lotsman.labels" .root }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{- define "lotsman.componentSelectorLabels" -}}
{{ include "lotsman.selectorLabels" .root }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Resolve an image reference: registry/repository:tag
Call as: (include "lotsman.image" (dict "root" . "repository" "lotsman-server" "tag" "..."))
*/}}
{{- define "lotsman.image" -}}
{{- $tag := .tag | default .root.Values.image.tag | default .root.Chart.AppVersion -}}
{{- printf "%s/%s:%s" .root.Values.image.registry .repository $tag -}}
{{- end }}

{{/*
Name of the Secret holding shared credentials (existing or chart-created).
*/}}
{{- define "lotsman.secretName" -}}
{{- if .Values.secret.existingSecret -}}
{{- .Values.secret.existingSecret -}}
{{- else -}}
{{- include "lotsman.fullname" . -}}
{{- end -}}
{{- end }}

{{/*
Control-plane ServiceAccount name.
*/}}
{{- define "lotsman.controlPlane.serviceAccountName" -}}
{{- if .Values.controlPlane.serviceAccount.create -}}
{{- default (include "lotsman.controlPlane.fullname" .) .Values.controlPlane.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.controlPlane.serviceAccount.name -}}
{{- end -}}
{{- end }}

{{/*
Agent ServiceAccount name.
*/}}
{{- define "lotsman.agent.serviceAccountName" -}}
{{- if .Values.agent.serviceAccount.create -}}
{{- default (include "lotsman.agent.fullname" .) .Values.agent.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.agent.serviceAccount.name -}}
{{- end -}}
{{- end }}

{{/*
Resolve the address an agent uses to dial the control-plane gateway. Explicit
value wins; otherwise fall back to the in-cluster gateway Service (only valid
when the control plane is deployed in this same release/namespace).
*/}}
{{- define "lotsman.agent.controlPlaneAddr" -}}
{{- if .Values.agent.controlPlaneAddr -}}
{{- .Values.agent.controlPlaneAddr -}}
{{- else -}}
{{- printf "%s.%s.svc:%v" (include "lotsman.controlPlane.fullname" .) .Release.Namespace .Values.controlPlane.gatewayService.port -}}
{{- end -}}
{{- end }}
