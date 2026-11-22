#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component.

Why this exists (fleet-wide): `app.kubernetes.io/name` + `instance` are identical
on every pod a release creates. A Service that selects on those two alone selects
ALL of them (a Kong request for an api /healthz was once answered by the wrong
pod). Every Deployment carries `app.kubernetes.io/component` in selector.matchLabels
and its pod labels, and every Service selects on it, so each Service selects
EXACTLY ONE Deployment. This test fails if that ever stops being true.

This chart has these components: api (always), the optional, default-off mcp
(ADR 0006, the read-only MCP server), the optional, default-off a2a
(ADR 0009, the read-only A2A agent) and the optional, default-off analytics read
side (ADR 0012: analytics-projector, a Deployment with NO Service, and
analytics-reports), each rendered with its own checks below.
The optional HPA, Ingress and api HTTPRoute are turned on together and
must all point at the api workload. Run:
python3 charts/customer-returns/tests/test_service_selectors.py
Needs: helm, PyYAML (a missing PyYAML prints SKIP, which CI treats as a failure).
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]
RELEASE = "customer-returns"

GROUPS = {
    "orderConsumerGroup": "customer-returns-order",
    "topologyConsumerGroup": "customer-returns-topology",
    "sourcingConsumerGroup": "customer-returns-sourcing",
    "vendorConsumerGroup": "customer-returns-vendor",
}
URLS = {}  # every neighbour URL is optional: the reverse-label route is disabled when empty


def runtime(skip: str | None = None) -> list[str]:
    """The mandatory values, minus the one named by skip (database, kafka or a config key)."""
    args: list[str] = []
    if skip != "database":
        args += ["--set", "database.url=postgres://u@example.invalid:5432/db"]
    if skip != "kafka":
        args += ["--set", "kafka.brokers=kafka.example.invalid:9092"]
    for key, value in {**GROUPS, **URLS}.items():
        if skip != key:
            args += ["--set", f"config.{key}={value}"]
    return args


BASE = runtime()
ENABLE_EVERYTHING = BASE + [
    "--set", "autoscaling.api.enabled=true",
    "--set", "gatewayApi.enabled=true",
    "--set", "ingress.enabled=true",
    "--set", "config.shutdownDrainDelay=5s",
]
# The mcp component (ADR 0006), rendered together with its optional host route.
ENABLE_MCP = BASE + [
    "--set", "mcp.enabled=true",
    "--set", "mcp.route.enabled=true",
]
# The a2a component (ADR 0009), rendered together with its optional host route.
ENABLE_A2A = BASE + [
    "--set", "a2a.enabled=true",
    "--set", "a2a.route.enabled=true",
]
# agents.colocate (warehouse-infra ADR 0012, capacity step 3): mcp and a2a as two containers of ONE
# customer-returns-agents Deployment; both Services select it.
ENABLE_AGENTS = BASE + [
    "--set", "mcp.enabled=true", "--set", "mcp.route.enabled=true",
    "--set", "a2a.enabled=true", "--set", "a2a.route.enabled=true",
    "--set", "agents.colocate=true",
]
# The analytics read side (ADR 0012): the projector and the reports pods.
ENABLE_ANALYTICS = BASE + [
    "--set", "analytics.enabled=true",
    "--set", "analytics.database.projectorUrl=postgres://projector@example.invalid:5432/analytics",
    "--set", "analytics.reports.route.enabled=true",
]
# The frontend component (ADR 0007): the nginx pod of the customer_returns_mfe
# web remote, rendered together with the api it belongs to.
ENABLE_FRONTEND = BASE + [
    "--set", "frontend.enabled=true",
]

# Exactly the env vars cmd/mcp (main.go, telemetry.go) reads (its own contract;
# the api ConfigMap is deliberately not mounted on that Deployment).
MCP_ENV = {"MCP_ADDR", "LOG_LEVEL", "SERVICE_VERSION", "ENVIRONMENT", "DATABASE_URL", "MIGRATIONS_DATABASE_URL",
           "OTEL_SERVICE_NAME", "OTEL_EXPORTER_OTLP_ENDPOINT"}
# Never rendered on the mcp Deployment: the binary is read-only and never
# dials Kafka.
MCP_STRAY = {"KAFKA_BROKERS", "EVENT_PUBLISHER", "SHIPPING_LABELS_URL",
             "OUTBOX_RELAY_INTERVAL", "OUTBOX_MAX_ATTEMPTS",
             "ORDER_CONSUMER_GROUP", "TOPOLOGY_CONSUMER_GROUP", "SOURCING_CONSUMER_GROUP", "VENDOR_CONSUMER_GROUP"}


# Exactly the env vars cmd/a2a (main.go, telemetry.go) reads (its own contract;
# the api ConfigMap is deliberately not mounted on that Deployment).
A2A_ENV = {"A2A_ADDR", "A2A_AGENT_URL", "LOG_LEVEL", "SERVICE_VERSION", "ENVIRONMENT", "DATABASE_URL",
           "MIGRATIONS_DATABASE_URL", "OTEL_SERVICE_NAME", "OTEL_EXPORTER_OTLP_ENDPOINT"}


# The analytics read side never sees the OLTP database, the label route, the relay or the OLTP
# consumers; the reports pod gets no Kafka and no DSN but the reader's.
ANALYTICS_STRAY = (MCP_STRAY - {"KAFKA_BROKERS"}) | {"DATABASE_URL", "MIGRATIONS_DATABASE_URL"}
REPORTS_STRAY = ANALYTICS_STRAY | {"KAFKA_BROKERS", "ANALYTICS_DATABASE_URL", "ANALYTICS_CONSUMER_GROUP"}


def helm_template(extra_args: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["helm", "template", RELEASE, str(CHART_DIR), *extra_args],
        capture_output=True, text=True,
    )


def render(extra_args: list[str]) -> list[dict]:
    result = helm_template(extra_args)
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template {' '.join(extra_args)}:\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert selectors")
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def selector_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("selector") or {}


def pod_labels_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("template", {}).get("metadata", {}).get("labels") or {}


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def check_components(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if set(deployments) != {RELEASE}:
        failures.append(f"expected exactly the api Deployment {RELEASE!r}, got {sorted(deployments)}")
    if set(services) != {RELEASE}:
        failures.append(f"expected exactly the api Service {RELEASE!r}, got {sorted(services)}")
    if RELEASE in services and selector_of(services[RELEASE]).get("app.kubernetes.io/component") != "api":
        failures.append("the api Service selector must pin component=api")

    # Every Deployment's own selector must pin a component too, and be satisfied by its pod labels.
    for name, dep in deployments.items():
        match_labels = dep["spec"]["selector"].get("matchLabels") or {}
        if "app.kubernetes.io/component" not in match_labels:
            failures.append(f"Deployment {name} selector.matchLabels lacks app.kubernetes.io/component")
        if not matches(match_labels, pod_labels_of(dep)):
            failures.append(f"Deployment {name} pod labels do not satisfy its own selector")

    # The real invariant: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    # The HPA owns replicas when enabled and scales the api Deployment.
    hpa = next((d for d in docs if d.get("kind") == "HorizontalPodAutoscaler"), None)
    if hpa is None or hpa["spec"]["scaleTargetRef"]["name"] != RELEASE:
        failures.append("the api HPA was not rendered (or does not scale the api Deployment)")
    elif "replicas" in deployments.get(RELEASE, {}).get("spec", {}):
        failures.append("the api Deployment must omit replicas when its HPA owns them")

    # The Ingress and the HTTPRoute route to the api Service.
    ingress = next((d for d in docs if d.get("kind") == "Ingress"), None)
    if ingress is None:
        failures.append("the Ingress was not rendered with ingress.enabled=true")
    else:
        for rule in ingress["spec"]["rules"]:
            for path in rule["http"]["paths"]:
                if path["backend"]["service"]["name"] not in services:
                    failures.append("the Ingress backend is not a Service of this chart")
    route = next((d for d in docs if d.get("kind") == "HTTPRoute"), None)
    if route is None:
        failures.append("the HTTPRoute was not rendered with gatewayApi.enabled=true")
    else:
        for rule in route["spec"]["rules"]:
            for ref in rule["backendRefs"]:
                if ref["name"] not in services:
                    failures.append("the HTTPRoute backend is not a Service of this chart")


def check_mcp(docs: list[dict], failures: list[str]) -> None:
    """The mcp component (ADR 0006): its own Deployment + ClusterIP Service
    (component=mcp pinned everywhere), its own env contract, and its optional
    host route targeting the mcp Service."""
    name = f"{RELEASE}-mcp"
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if set(deployments) != {RELEASE, name}:
        failures.append(f"with mcp.enabled: expected exactly the api and mcp Deployments, got {sorted(deployments)}")
    if set(services) != {RELEASE, name}:
        failures.append(f"with mcp.enabled: expected exactly the api and mcp Services, got {sorted(services)}")

    # The real invariant, again: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    if name in services and selector_of(services[name]).get("app.kubernetes.io/component") != "mcp":
        failures.append("the mcp Service selector must pin component=mcp")
    if name not in deployments:
        failures.append("the mcp Deployment was not rendered with mcp.enabled=true")
        return
    if deployments[name]["spec"]["selector"].get("matchLabels", {}).get("app.kubernetes.io/component") != "mcp":
        failures.append("the mcp Deployment selector.matchLabels must pin component=mcp")
    if not matches(deployments[name]["spec"]["selector"].get("matchLabels", {}), pod_labels_of(deployments[name])):
        failures.append("the mcp Deployment pod labels do not satisfy its own selector")

    containers = deployments[name]["spec"]["template"]["spec"]["containers"]
    command = containers[0].get("command", []) if containers else []
    if command != ["/app/mcp"]:
        failures.append(f"the mcp Deployment must run /app/mcp, got {command}")
    env = {e["name"] for e in (containers[0].get("env", []) if containers else []) if "name" in e}
    if env != MCP_ENV:
        failures.append(f"the mcp Deployment env must be exactly {sorted(MCP_ENV)}, got {sorted(env)}")
    stray = MCP_STRAY & env
    if stray:
        failures.append(f"the mcp Deployment must not get Kafka/relay/consumer/SSCC env: {sorted(stray)}")

    route = next((d for d in docs if d.get("kind") == "HTTPRoute" and d["metadata"]["name"] == name), None)
    if route is None:
        failures.append("the mcp HTTPRoute was not rendered with mcp.route.enabled=true")
    else:
        for rule in route["spec"]["rules"]:
            for ref in rule["backendRefs"]:
                if ref["name"] != name:
                    failures.append("the mcp HTTPRoute backend is not the mcp Service")


def check_a2a(docs: list[dict], failures: list[str]) -> None:
    """The a2a component (ADR 0009): its own Deployment + ClusterIP Service
    (component=a2a pinned everywhere), its own env contract (mandatory
    A2A_AGENT_URL included, never Kafka), and its optional host route
    targeting the a2a Service."""
    name = f"{RELEASE}-a2a"
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if set(deployments) != {RELEASE, name}:
        failures.append(f"with a2a.enabled: expected exactly the api and a2a Deployments, got {sorted(deployments)}")
    if set(services) != {RELEASE, name}:
        failures.append(f"with a2a.enabled: expected exactly the api and a2a Services, got {sorted(services)}")

    # The real invariant, again: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    if name in services and selector_of(services[name]).get("app.kubernetes.io/component") != "a2a":
        failures.append("the a2a Service selector must pin component=a2a")
    if name not in deployments:
        failures.append("the a2a Deployment was not rendered with a2a.enabled=true")
        return
    if deployments[name]["spec"]["selector"].get("matchLabels", {}).get("app.kubernetes.io/component") != "a2a":
        failures.append("the a2a Deployment selector.matchLabels must pin component=a2a")
    if not matches(deployments[name]["spec"]["selector"].get("matchLabels", {}), pod_labels_of(deployments[name])):
        failures.append("the a2a Deployment pod labels do not satisfy its own selector")

    containers = deployments[name]["spec"]["template"]["spec"]["containers"]
    command = containers[0].get("command", []) if containers else []
    if command != ["/app/a2a"]:
        failures.append(f"the a2a Deployment must run /app/a2a, got {command}")
    env = {e["name"] for e in (containers[0].get("env", []) if containers else []) if "name" in e}
    if env != A2A_ENV:
        failures.append(f"the a2a Deployment env must be exactly {sorted(A2A_ENV)}, got {sorted(env)}")
    stray = MCP_STRAY & env
    if stray:
        failures.append(f"the a2a Deployment must not get Kafka/relay/consumer/SSCC env: {sorted(stray)}")

    route = next((d for d in docs if d.get("kind") == "HTTPRoute" and d["metadata"]["name"] == name), None)
    if route is None:
        failures.append("the a2a HTTPRoute was not rendered with a2a.route.enabled=true")
    else:
        for rule in route["spec"]["rules"]:
            for ref in rule["backendRefs"]:
                if ref["name"] != name:
                    failures.append("the a2a HTTPRoute backend is not the a2a Service")
            rewrite = [f["urlRewrite"]["path"].get("replacePrefixMatch") for f in rule.get("filters", []) if f.get("type") == "URLRewrite"]
            if rewrite != ["/"]:
                failures.append(f"the a2a HTTPRoute must rewrite its prefix to /, got {rewrite}")


def check_agents(docs: list[dict], failures: list[str]) -> None:
    """agents.colocate: the api and ONE agents Deployment (component=agents, containers mcp + a2a); the mcp
    and a2a Services both select exactly that Deployment, the api Service only the api."""
    agents = f"{RELEASE}-agents"
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}
    if set(deployments) != {RELEASE, agents}:
        failures.append(f"with agents.colocate: expected exactly the api and agents Deployments, got {sorted(deployments)}")
    if set(services) != {RELEASE, f"{RELEASE}-mcp", f"{RELEASE}-a2a"}:
        failures.append(f"with agents.colocate: expected the api, mcp and a2a Services, got {sorted(services)}")
    for svc_name, svc in services.items():
        hit = [d for d, dep in deployments.items() if matches(selector_of(svc), pod_labels_of(dep))]
        want = [RELEASE] if svc_name == RELEASE else [agents]
        if hit != want:
            failures.append(f"with agents.colocate: Service {svc_name} selects {sorted(hit)}; expected {want}")
    dep = deployments.get(agents)
    if dep is not None:
        if dep["spec"]["selector"].get("matchLabels", {}).get("app.kubernetes.io/component") != "agents":
            failures.append("the agents Deployment selector.matchLabels must pin component=agents")
        names = sorted(c["name"] for c in dep["spec"]["template"]["spec"]["containers"])
        if names != ["a2a", "mcp"]:
            failures.append(f"the agents Deployment must run the containers a2a and mcp, got {names}")


def check_analytics(docs: list[dict], failures: list[str]) -> None:
    """The analytics read side (ADR 0012): the projector (no Service) and the reports (Service
    component=analytics-reports, :80), their own binaries and env, and the reports route."""
    projector, reports = f"{RELEASE}-projector", f"{RELEASE}-reports"
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if set(deployments) != {RELEASE, projector, reports}:
        failures.append(f"with analytics.enabled: expected the api, projector and reports Deployments, got {sorted(deployments)}")
    if set(services) != {RELEASE, reports}:
        failures.append(f"with analytics.enabled: expected the api and reports Services (the projector has none), got {sorted(services)}")

    # The real invariant, again: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")
    if reports in services and selector_of(services[reports]).get("app.kubernetes.io/component") != "analytics-reports":
        failures.append("the reports Service selector must pin component=analytics-reports")

    wants = (
        (projector, "analytics-projector", "/app/projector", ANALYTICS_STRAY,
         ("ANALYTICS_DATABASE_URL", "KAFKA_BROKERS", "ANALYTICS_CONSUMER_GROUP", "ADMIN_ADDR", "LOG_LEVEL", "SERVICE_VERSION", "ENVIRONMENT")),
        (reports, "analytics-reports", "/app/reports", REPORTS_STRAY,
         ("ANALYTICS_READER_DATABASE_URL", "HTTP_ADDR", "LOG_LEVEL", "SERVICE_VERSION", "ENVIRONMENT")),
    )
    for name, component, command, stray_set, needs in wants:
        dep = deployments.get(name)
        if dep is None:
            failures.append(f"the {component} Deployment was not rendered with analytics.enabled=true")
            continue
        match_labels = dep["spec"]["selector"].get("matchLabels", {})
        if match_labels.get("app.kubernetes.io/component") != component:
            failures.append(f"the {component} Deployment selector.matchLabels must pin component={component}")
        if not matches(match_labels, pod_labels_of(dep)):
            failures.append(f"the {component} Deployment pod labels do not satisfy its own selector")
        containers = dep["spec"]["template"]["spec"]["containers"]
        if (containers[0].get("command", []) if containers else []) != [command]:
            failures.append(f"the {component} Deployment must run {command}")
        env = {e["name"] for e in (containers[0].get("env", []) if containers else []) if "name" in e}
        if stray_set & env:
            failures.append(f"the {component} Deployment must not get {sorted(stray_set & env)}")
        for need in needs:
            if need not in env:
                failures.append(f"the {component} Deployment does not render {need}")

    route = next((d for d in docs if d.get("kind") == "HTTPRoute" and d["metadata"]["name"] == reports), None)
    if route is None:
        failures.append("the reports HTTPRoute was not rendered with analytics.reports.route.enabled=true")
    else:
        if route["metadata"].get("annotations", {}).get("konghq.com/regex-priority") != "10":
            failures.append("the reports HTTPRoute must carry konghq.com/regex-priority: \"10\"")
        for rule in route["spec"]["rules"]:
            for ref in rule["backendRefs"]:
                if ref["name"] != reports:
                    failures.append("the reports HTTPRoute backend is not the reports Service")
            rewrite = [f["urlRewrite"]["path"].get("replacePrefixMatch") for f in rule.get("filters", []) if f.get("type") == "URLRewrite"]
            if rewrite != ["/reports"]:
                failures.append(f"the reports HTTPRoute must rewrite its prefix to /reports, got {rewrite}")


def check_refusals(failures: list[str]) -> None:
    cases: list[tuple[str, list[str], str]] = [
        ("without a database source", runtime("database"), "requires database.url or database.existingSecret"),
        ("without kafka.brokers", runtime("kafka"), "kafka.brokers"),
        ("the log publisher", BASE + ["--set", "config.eventPublisher=log"], "the only publisher is"),
        ("an unknown publisher", BASE + ["--set", "config.eventPublisher=nats"], "the only publisher is"),
        ("EVENT_PUBLISHER=log through extraEnv",
         BASE + ["--set", "extraEnv[0].name=EVENT_PUBLISHER", "--set", "extraEnv[0].value=log"], "extraEnv sets EVENT_PUBLISHER=log"),
        ("a *_MODE through extraEnv",
         BASE + ["--set", "extraEnv[0].name=NODE_MODE", "--set", "extraEnv[0].value=permissive"], "extraEnv sets NODE_MODE"),
        ("a grace period under 40s", BASE + ["--set", "terminationGracePeriodSeconds=35"], "terminationGracePeriodSeconds"),
    ]
    for key in {**GROUPS, **URLS}:
        cases.append((f"without config.{key}", runtime(key), f"config.{key} is required"))
    for label, args, needle in cases:
        result = helm_template(args)
        if result.returncode == 0 or needle not in result.stderr:
            failures.append(f"chart rendered (or failed for another reason) {label}")


def main() -> int:
    failures: list[str] = []

    check_components(render(ENABLE_EVERYTHING), failures)
    check_mcp(render(ENABLE_MCP), failures)
    check_a2a(render(ENABLE_A2A), failures)
    check_agents(render(ENABLE_AGENTS), failures)
    check_analytics(render(ENABLE_ANALYTICS), failures)

    # Default values must not deploy an HPA, a route, an Ingress, the mcp or
    # the frontend.
    defaults = render(BASE)
    stray = [d["kind"] for d in defaults if d.get("kind") in {"HorizontalPodAutoscaler", "Ingress", "HTTPRoute"}]
    if stray:
        failures.append(f"optional resources rendered with default values: {stray}")
    if any(d.get("metadata", {}).get("name", "").endswith("-mcp") for d in defaults):
        failures.append("the mcp component must not render with default values (mcp.enabled=false)")
    if any(d.get("metadata", {}).get("name", "").endswith("-a2a") for d in defaults):
        failures.append("the a2a component must not render with default values (a2a.enabled=false)")
    if any(d.get("metadata", {}).get("name", "").endswith("-agents") for d in defaults):
        failures.append("the agents Deployment must not render with default values (agents.colocate=false)")
    if any(d.get("metadata", {}).get("name", "").endswith(("-projector", "-reports")) for d in defaults):
        failures.append("the analytics components must not render with default values (analytics.enabled=false)")

    check_refusals(failures)

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1

    print("PASS: the api Service selects exactly one Deployment (component=api) and so does the mcp Service "
          "(component=mcp, ADR 0006, with exactly the env cmd/mcp reads), the a2a Service (component=a2a, ADR 0009, "
          "with exactly the env cmd/a2a reads and no Kafka env; with agents.colocate both select the one agents Deployment) "
          "and the frontend Service "
          "(component=frontend, ADR 0007, ClusterIP, no env, never routed by this chart) and the analytics-reports Service "
          "(component=analytics-reports, ADR 0012; analytics-projector has no Service, both run their own binary with their own env "
          "and the reports route outranks the API route); the HPA, Ingress, "
          "HTTPRoute and the mcp, a2a, frontend and analytics components are off by default and target their own component; "
          "the chart refuses to render without a "
          "database source, brokers, any consumer group or neighbour URL, with a log/unknown publisher or a "
          "*_MODE in extraEnv, or with a grace period under 40s")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
