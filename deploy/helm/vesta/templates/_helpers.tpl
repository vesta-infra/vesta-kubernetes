{{- define "vesta.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "vesta.fullname" -}}
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

{{- define "vesta.labels" -}}
app.kubernetes.io/name: {{ include "vesta.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: vesta
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "vesta.selectorLabels" -}}
app.kubernetes.io/name: {{ include "vesta.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "vesta.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "vesta.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Read a section's `enabled` flag without assuming the section exists.

`helm upgrade --reuse-values` does NOT layer the new chart's defaults over the previous
release's values -- it uses those values alone. Every key introduced in a later chart version
is therefore absent during such an upgrade, and a plain `.Values.thing.enabled` fails with
"nil pointer evaluating interface {}.enabled". That renders fine on a fresh install, passes
`helm lint`, and passes CI, so it surfaces only in front of somebody upgrading a running
instance. 0.10.0 shipped exactly that for the new `activator` key.

`| default X` is not good enough for a boolean: `default` treats false as empty, so it would
silently re-enable something deliberately turned off. hasKey is the only way to tell "set to
false" apart from "not set at all".

Returns "true" or "". Call as:

  {{- if eq (include "vesta.enabled" (dict "Values" .Values "section" "postgres" "default" false)) "true" }}

hack/check-reuse-values.sh fails the build if any template reads a section directly.
*/}}
{{- define "vesta.enabled" -}}
{{- $section := index .Values .section | default dict -}}
{{- if hasKey $section "enabled" -}}
{{- if $section.enabled -}}true{{- end -}}
{{- else if .default -}}
true
{{- end -}}
{{- end }}

{{- define "vesta.activatorImage" -}}
{{- $a := .Values.activator | default dict -}}
{{- $img := $a.image | default dict -}}
{{- $repo := $img.repository | default "ghcr.io/vesta-infra/kubernetes-activator" -}}
{{- $tag := $img.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s" $repo $tag -}}
{{- end }}

{{- define "vesta.selfUpdateHelmImage" -}}
{{- $s := .Values.selfUpdate | default dict -}}
{{- $s.helmImage | default "alpine/helm:3.16.3" -}}
{{- end }}

{{/*
Read a nested value that may not exist, with a fallback.

Same reason as vesta.enabled: a section introduced after the release somebody is upgrading
from is entirely absent under --reuse-values, so any read below it is a nil dereference.

  {{ include "vesta.value" (dict "Values" .Values "path" (list "crdManagement" "timeoutSeconds") "default" 300) }}
*/}}
{{- define "vesta.value" -}}
{{- $cur := .Values -}}
{{- $found := true -}}
{{- range .path -}}
  {{- if and $found (kindIs "map" $cur) (hasKey $cur .) -}}
    {{- $cur = index $cur . -}}
  {{- else -}}
    {{- $found = false -}}
  {{- end -}}
{{- end -}}
{{- if and $found (not (kindIs "invalid" $cur)) -}}{{ $cur }}{{- else -}}{{ .default }}{{- end -}}
{{- end }}
