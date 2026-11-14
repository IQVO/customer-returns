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
