{{- define "operator.name" -}}
{{- printf "%s-operator" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- define "operator.sa" -}}
{{- default (include "operator.name" .) .Values.serviceAccount.name -}}
{{- end -}}
