{{/*
Expand the name of the chart.
*/}}
{{- define "seasonfill.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to
this by the DNS naming spec (RFC 1123). If the release name contains the
chart name it is used as-is to avoid double-prefixed names like
`seasonfill-seasonfill`.
*/}}
{{- define "seasonfill.fullname" -}}
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

{{/*
Chart name and version as used by the chart label.
*/}}
{{- define "seasonfill.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels — applied to every rendered resource.
*/}}
{{- define "seasonfill.labels" -}}
helm.sh/chart: {{ include "seasonfill.chart" . }}
{{ include "seasonfill.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/part-of: {{ .Chart.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels — used in matchLabels and Service selectors. Stable
across upgrades: only name + instance. Adding/removing keys here is a
breaking change because Deployment selectors are immutable.
*/}}
{{- define "seasonfill.selectorLabels" -}}
app.kubernetes.io/name: {{ include "seasonfill.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Name of the ServiceAccount to use. If serviceAccount.create is true and
no explicit name is set, derive from the fullname. Otherwise honor the
operator's choice (or fall back to the namespace default).
*/}}
{{- define "seasonfill.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "seasonfill.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Convert an instance name into the upper-snake-case fragment used in
env-var keys: dashes become underscores, the whole string is upper-cased.
Example: "sonarr-anime" -> "SONARR_ANIME".
*/}}
{{- define "seasonfill.envName" -}}
{{- . | replace "-" "_" | upper }}
{{- end }}

{{/*
Name of the PersistentVolumeClaim backing /data when sqlite persistence
is enabled. Derived from fullname with a `-data` suffix so it is stable
across upgrades and easy to identify alongside the Deployment.
*/}}
{{- define "seasonfill.pvcName" -}}
{{- printf "%s-data" (include "seasonfill.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Name of the frontend (web) child resources. Derived from the chart
name with a `-web` suffix so it pairs visibly with the backend.
*/}}
{{- define "seasonfill.webName" -}}
{{- printf "%s-web" (include "seasonfill.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully-qualified name of the frontend Deployment / Service / ConfigMap.
Pattern matches `seasonfill.fullname` so operators can predict the
resource names without re-rendering the chart.
*/}}
{{- define "seasonfill.webFullname" -}}
{{- printf "%s-web" (include "seasonfill.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Web selector labels — used in matchLabels and Service selectors. Adds
`app.kubernetes.io/component: web` to disambiguate from the backend
pods, which carry the same `name` + `instance` pair. Adding/removing
keys here is a breaking change for the web Deployment selector.
*/}}
{{- define "seasonfill.webSelectorLabels" -}}
app.kubernetes.io/name: {{ include "seasonfill.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: web
{{- end }}

{{/*
Common labels applied to every web resource. Mirrors `seasonfill.labels`
but with the web selector labels inlined and a `component: web` value
added under the standard Kubernetes recommended labels.
*/}}
{{- define "seasonfill.webLabels" -}}
helm.sh/chart: {{ include "seasonfill.chart" . }}
{{ include "seasonfill.webSelectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/part-of: {{ .Chart.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
