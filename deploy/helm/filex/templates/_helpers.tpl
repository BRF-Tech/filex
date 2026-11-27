{{/* Chart name */}}
{{- define "filex.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Fully-qualified release name */}}
{{- define "filex.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "filex.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Common labels */}}
{{- define "filex.labels" -}}
app.kubernetes.io/name: {{ include "filex.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{/* Selector labels */}}
{{- define "filex.selectorLabels" -}}
app.kubernetes.io/name: {{ include "filex.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Image ref */}}
{{- define "filex.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}

{{/*
The Ingress paths of one host: the base (or /), and under a base the two OAuth
discovery documents an MCP client asks for, which RFC 8414 and RFC 9728 put
BEFORE the base (/.well-known/oauth-protected-resource/<base>/api/ai/mcp,
/.well-known/oauth-authorization-server/<base>; docs/MCP.md -> Connecting
with OAuth). At the root of the host the base path covers them already.
*/}}
{{- define "filex.ingressPaths" -}}
{{- $paths := list (.Values.basePath | default "/") -}}
{{- if .Values.basePath -}}
{{- $paths = append $paths (printf "/.well-known/oauth-authorization-server%s" .Values.basePath) -}}
{{- $paths = append $paths (printf "/.well-known/oauth-protected-resource%s" .Values.basePath) -}}
{{- end -}}
{{- range $paths }}
- path: {{ . }}
  pathType: Prefix
  backend:
    service:
      name: {{ include "filex.fullname" $ }}
      port:
        name: http
{{- end }}
{{- end -}}
