{{/* agent-infra 通用模板助手 */}}

{{- define "agent-infra.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "agent-infra.namespace" -}}
{{- .Values.namespace.name | default "agent-runtime-system" -}}
{{- end -}}

{{- define "agent-infra.labels" -}}
app.kubernetes.io/name: {{ include "agent-infra.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{- define "agent-infra.operatorName" -}}agent-infra-operator{{- end -}}
{{- define "agent-infra.workerName" -}}agent-infra-worker{{- end -}}
{{- define "agent-infra.natsName" -}}agent-infra-nats{{- end -}}
{{- define "agent-infra.temporalName" -}}agent-infra-temporal{{- end -}}
{{- define "agent-infra.postgresName" -}}agent-infra-postgres{{- end -}}
