{{/*
Expand the name of the chart.
*/}}
{{- define "customer-returns.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "customer-returns.fullname" -}}
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
{{- define "customer-returns.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "customer-returns.labels" -}}
helm.sh/chart: {{ include "customer-returns.chart" . }}
{{ include "customer-returns.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. The Deployment and the Service ALSO pin
app.kubernetes.io/component (api), so a component added later cannot be
selected by accident.
*/}}
{{- define "customer-returns.selectorLabels" -}}
app.kubernetes.io/name: {{ include "customer-returns.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "customer-returns.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "customer-returns.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "customer-returns.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "customer-returns.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Image reference.
*/}}
{{- define "customer-returns.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
The non-secret environment of cmd/api, as a YAML map: the single source for the
ConfigMap (envFrom) and for the container env, so the two cannot drift. Every
key is a variable cmd/api reads (tests/render_test.sh checks both directions).
DATABASE_URL and MIGRATIONS_DATABASE_URL never appear here: they come from a Secret.
*/}}
{{- define "customer-returns.configData" -}}
HTTP_ADDR: {{ .Values.config.httpAddr | quote }}
LOG_LEVEL: {{ .Values.config.logLevel | quote }}
EVENT_PUBLISHER: {{ .Values.config.eventPublisher | quote }}
KAFKA_BROKERS: {{ .Values.kafka.brokers | quote }}
ORDER_CONSUMER_GROUP: {{ .Values.config.orderConsumerGroup | quote }}
TOPOLOGY_CONSUMER_GROUP: {{ .Values.config.topologyConsumerGroup | quote }}
SOURCING_CONSUMER_GROUP: {{ .Values.config.sourcingConsumerGroup | quote }}
VENDOR_CONSUMER_GROUP: {{ .Values.config.vendorConsumerGroup | quote }}
{{- with .Values.config.shippingLabelsUrl }}
SHIPPING_LABELS_URL: {{ . | quote }}
{{- end }}
OUTBOX_MAX_ATTEMPTS: {{ .Values.config.outboxMaxAttempts | quote }}
{{- with .Values.config.outboxRelayInterval }}
OUTBOX_RELAY_INTERVAL: {{ . | quote }}
{{- end }}
{{- with .Values.config.shutdownDrainDelay }}
SHUTDOWN_DRAIN_DELAY: {{ . | quote }}
{{- end }}
{{- end -}}

{{/*
MANDATORY: cmd/api exits at boot without DATABASE_URL; there is no in-memory
mode. Fails the render naming the values instead of crash-looping.
*/}}
{{- define "customer-returns.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "customer-returns requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: cmd/api exits at boot without KAFKA_BROKERS and rejects
EVENT_PUBLISHER=log. There is no default broker and no log sink.
*/}}
{{- define "customer-returns.requireKafka" -}}
{{- $_ := required "customer-returns requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it." .Values.kafka.brokers -}}
{{- if ne (toString .Values.config.eventPublisher) "kafka" -}}
{{- fail (printf "config.eventPublisher is %q: the only publisher is \"kafka\"; the binary rejects any other EVENT_PUBLISHER at boot." (toString .Values.config.eventPublisher)) -}}
{{- end -}}
{{- end -}}

{{/*
The four consumers always run, each under a STABLE consumer group id that
comes from values (cmd/api exits at boot without ORDER_CONSUMER_GROUP,
TOPOLOGY_CONSUMER_GROUP, SOURCING_CONSUMER_GROUP and VENDOR_CONSUMER_GROUP).
A stable id is what lets a rescheduled pod resume from its committed offsets;
never derive it from the pod name.
*/}}
{{- define "customer-returns.requireConsumerGroups" -}}
{{- range $c := list (dict "key" "orderConsumerGroup" "env" "ORDER_CONSUMER_GROUP") (dict "key" "topologyConsumerGroup" "env" "TOPOLOGY_CONSUMER_GROUP") (dict "key" "sourcingConsumerGroup" "env" "SOURCING_CONSUMER_GROUP") (dict "key" "vendorConsumerGroup" "env" "VENDOR_CONSUMER_GROUP") -}}
{{- if not (index $.Values.config $c.key) -}}
{{- fail (printf "config.%s is required (%s): the consumer always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE, service-prefixed id, never the pod name." $c.key $c.env) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
The mcp component's fullname (<release>-customer-returns-mcp): the mcp
Deployment and Service.
*/}}
{{- define "customer-returns.mcpFullname" -}}
{{- include "customer-returns.fullname" . }}-mcp
{{- end }}

{{/*
The a2a component's fullname (<release>-customer-returns-a2a): the a2a
Deployment and Service.
*/}}
{{- define "customer-returns.a2aFullname" -}}
{{- include "customer-returns.fullname" . }}-a2a
{{- end }}

{{/*
No mode switch, no second publisher, no duplicated env: the chart renders
otel and environment vars itself and refuses anything that would shadow it.
*/}}
{{- define "customer-returns.requireNoInMemoryEnv" -}}
{{- $owned := list "ENVIRONMENT" "SERVICE_VERSION" "OTEL_SERVICE_NAME" "OTEL_EXPORTER_OTLP_ENDPOINT" -}}
{{- range .Values.extraEnv -}}
{{- $name := toString .name -}}
{{- if has $name $owned -}}
{{- fail (printf "extraEnv sets %s: the chart renders it itself (otel.*, environment, image.tag); set the value there, never twice." $name) -}}
{{- end -}}
{{- $value := toString (default "" .value) -}}
{{- if and (eq $name "EVENT_PUBLISHER") (ne $value "kafka") -}}
{{- fail (printf "extraEnv sets EVENT_PUBLISHER=%s: the only publisher is \"kafka\"." $value) -}}
{{- end -}}
{{- if and (eq $name "ORDER_CONSUMER_GROUP") (eq $value "in-memory") -}}
{{- fail (printf "extraEnv sets %s=%s: in-memory is not a group id; the consumers run on real Kafka." $name $value) -}}
{{- end -}}
{{- if and (hasSuffix "_MODE" $name) (ne $value "enforcing") -}}
{{- fail (printf "extraEnv sets %s=%s: *_MODE variables are not supported; customer-returns runs on real Postgres and real Kafka only and has no mode switch." $name $value) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
The drain delay must be whole seconds and the pod's grace period must cover it
(SHUTDOWN_DRAIN_DELAY + HTTP drain 10s + consumers 10s + margin).
*/}}
{{- define "customer-returns.requireGracePeriod" -}}
{{- $drain := .Values.config.shutdownDrainDelay | toString -}}
{{- if eq $drain "" -}}{{/* unset: the binary default is 5s and the check runs on it */}}
{{- $drain = "5s" -}}
{{- else if not (regexMatch "^[0-9]+s$" $drain) -}}
{{- fail (printf "config.shutdownDrainDelay is %q: use whole seconds such as \"5s\" so the chart can check terminationGracePeriodSeconds against it." $drain) -}}
{{- end -}}
{{- $need := add (int (trimSuffix "s" $drain)) 30 -}}
{{- if lt $need 40 -}}{{- $need = 40 -}}{{- end -}}
{{- if lt (int .Values.terminationGracePeriodSeconds) $need -}}
{{- fail (printf "terminationGracePeriodSeconds is %v: want at least %v (SHUTDOWN_DRAIN_DELAY %vs + HTTP drain 10s + consumers 10s + margin); a shorter grace period SIGKILLs the consumers mid-drain." .Values.terminationGracePeriodSeconds $need $drain) -}}
{{- end -}}
{{- end -}}

{{/*
Every render-time requirement of the real runtime, in one include. The api
Deployment includes it, so a mis-set release fails `helm template` / `helm install`.
*/}}
{{- define "customer-returns.requireRealRuntime" -}}
{{- include "customer-returns.requireDatabase" . -}}
{{- include "customer-returns.requireKafka" . -}}
{{- include "customer-returns.requireConsumerGroups" . -}}
{{- include "customer-returns.requireNoInMemoryEnv" . -}}
{{- include "customer-returns.requireGracePeriod" . -}}
{{- end -}}

{{/*
Fully qualified names of the analytics read side (ADR 0012): the projector
Deployment (no Service: it serves only its admin probes) and the reports
Deployment/Service. Components analytics-projector and analytics-reports keep
every Service selecting exactly one Deployment.
*/}}
{{- define "customer-returns.projectorFullname" -}}
{{- include "customer-returns.fullname" . }}-projector
{{- end }}

{{- define "customer-returns.reportsFullname" -}}
{{- include "customer-returns.fullname" . }}-reports
{{- end }}

{{/*
Name of the Secret holding the analytical DSNs: the operator's own
(analytics.database.existingSecret, keys ANALYTICS_DATABASE_URL and
ANALYTICS_READER_DATABASE_URL) or the one this chart creates.
*/}}
{{- define "customer-returns.analyticsSecretName" -}}
{{- if .Values.analytics.database.existingSecret }}
{{- .Values.analytics.database.existingSecret }}
{{- else }}
{{- include "customer-returns.fullname" . }}-analytics
{{- end }}
{{- end }}

{{/*
analytics.enabled needs an analytical DSN source (both binaries refuse to boot
without one), the Kafka brokers (the projector consumes
retailnetwork.customer-returns.events) and a stable consumer group. Fail
at render instead of crash-looping.
*/}}
{{- define "customer-returns.requireAnalytics" -}}
{{- if not (or .Values.analytics.database.projectorUrl .Values.analytics.database.existingSecret) -}}
{{- fail "analytics.enabled is true but neither analytics.database.projectorUrl nor analytics.database.existingSecret is set (ANALYTICS_DATABASE_URL): the projector and the reports refuse to boot without an analytical database." -}}
{{- end -}}
{{- if not .Values.kafka.brokers -}}
{{- fail "analytics.enabled is true but kafka.brokers is empty (KAFKA_BROKERS): the projector consumes retailnetwork.customer-returns.events." -}}
{{- end -}}
{{- if not .Values.analytics.projector.consumerGroup -}}
{{- fail "analytics.projector.consumerGroup is required (ANALYTICS_CONSUMER_GROUP): use the STABLE id customer-returns-analytics, never the pod name." -}}
{{- end -}}
{{- end -}}
