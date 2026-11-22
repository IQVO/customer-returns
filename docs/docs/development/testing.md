---
id: testing
title: Testing
sidebar_label: Testing
sidebar_position: 1
description: "The customer-returns test pyramid: unit, handler and use-case tests, godog scenarios, integration with testcontainers, contract, mutation and architecture fitness tests, and the CI jobs."
---

# Testing

The pyramid as it stands on `develop`. Everything except `make integration` runs without Docker, Postgres or Kafka.

| Layer | Where | How many | Runs with |
| --- | --- | --- | --- |
| Unit (domain, application, adapters) | `internal/domain/**`, `internal/application/**`, `internal/adapters/**`, `internal/bootretry`, `cmd/*/main_test.go` | 127 `_test.go` files, 20 of them `*_integration_test.go` | `make test` |
| HTTP handler tests (no database) | `internal/adapters/inbound/http/*_test.go` | handlers, readiness, NUL rejection, idempotency capture and key, replay classes, log sanitising | `make test` |
| BDD acceptance (godog) | `features/*.feature` | **3 feature files, 13 scenarios** | `make bdd` (also part of `make test`) |
| Integration (testcontainers) | files tagged `integration` under `cmd/{api,mcp,a2a,projector,reports}` and `internal/adapters/{inbound/http,inbound/kafka,outbound/postgres,outbound/outbox,outbound/kafka,outbound/analyticsstore}` | 20 `*_integration_test.go` files | `make integration` |
| Contract | pinned producer specs, event catalogue vs `apis/asyncapi.yaml`, MCP tool surface, CloudEvents encoder/decoder, Spectral lint of both specs | see below | `make test`, `make arch-test`, CI `pinned-contracts`, `api-lint` |
| Mutation | `internal/domain/**` with gremlins | thresholds in `.gremlins.yaml` | `make mutation`, `make mutation-full` |
| Architecture fitness | `internal/architecture/` | 16 test files, 43 `Test*` functions | `make arch-test` |

## Unit and use-case tests

The domain packages (`returnauthorization`, `network`, `returnsperformance`, `shared`) have table tests for every
invariant and for `decide`; `internal/application/usecases` has `labels_test.go`, `labels_expiry_test.go`,
`cancel_expiry_test.go` and the use-case coverage tests on in-memory test doubles. The in-memory store under
`internal/adapters/outbound/memory` exists for tests only: a fitness test (`TestRuntimeBinaryHasNoInMemoryAdapters`)
fails if any `cmd/**` binary imports it.

`make coverage` runs the whole suite with the race detector and enforces a **90 % gate** over
`./internal/domain/...` and `./internal/application/...` (`COVERAGE_PKGS` and `COVERAGE_THRESHOLD` in the `Makefile`;
CI runs the same target, so the scope cannot drift).

## godog features

| File | Scenarios |
| --- | --- |
| `features/authorize_returns.feature` | 4 |
| `features/transit.feature` | 3 |
| `features/returns_performance.feature` | 6 |
| **Total** | **13** |

The steps (`features/steps_test.go`, `features/analytics_steps_test.go`, `features/features_test.go`) drive the real use
cases and the real REST router over in-memory adapters, and feed the consumers with CloudEvents built from the
producers' contracts (customer-ordering, node-topology, order-sourcing, vendor-connectivity). Run them alone with
`make bdd` (`go test ./... -run TestFeatures -v`).

## Integration tests

`make integration` runs `go test -tags=integration ./... -race -count=1`. It needs only Docker: every test boots its
own Postgres and Kafka with testcontainers, so there is no `DATABASE_URL`, no docker compose and no skip gate. Two
fitness tests enforce this (`TestPostgresIntegrationTestsUseTestcontainers`, `TestKafkaIntegrationTestsUseTestcontainers`).

- `cmd/api/e2e_integration_test.go` starts the real `run` entry point against the containers and walks the REST API,
  the four consumers and the relay end to end; `labels_stub_integration_test.go` runs it against a reverse-label stub
  and `idempotency_integration_test.go` the `Idempotency-Key` paths.
- `cmd/mcp`, `cmd/a2a`, `cmd/projector` and `cmd/reports` have their own `main_integration_test.go`.
- `internal/adapters/outbound/postgres` covers the repositories, the inbox contract, and that an outbox row written
  under a span stores its `traceparent`; `outbound/analyticsstore` the analytical store against real Postgres;
  `inbound/http` the idempotency poison path and the replay classes.
- `internal/adapters/outbound/outbox` covers the relay and the attempt cap; `outbound/kafka` the sink and writers;
  `inbound/kafka` the consumers and dead-lettering.

## Contract tests

- Consumer contract tests against the producers' pinned specs (`contracts/pinned.json`, CI job `pinned-contracts`):
  the four event edges in `internal/adapters/inbound/kafka` (E1-E4) and the label route in
  `internal/adapters/outbound/labels` (R1-R4). See [Consumer contract testing](./contract-testing.md).

- `TestEventCatalogueMatchesContract` (`internal/architecture/catalogue_fitness_test.go`, a fleet-managed file) checks
  that every `com.warehouse.(wms|wes).<service>...` or `com.retailnetwork.<domain>.<service>...` type that
  `apis/asyncapi.yaml` declares for this module appears in the ADR catalogue. customer-returns's types are
  `com.retailnetwork.commerce.customer-returns...`; its four published types are checked against ADR 0004, and the types
  it only consumes (other services' segments) are not required there. `TestEventCatalogueDetector` pins both type
  shapes. A new event still changes the domain struct, `apis/asyncapi.yaml` and the ADR 0004 `type:` line in one commit
  (CLAUDE.md); the test now fails the build if the ADR line is missing.
- `TestToolSurface` (`internal/adapters/inbound/mcp/governance_test.go`) pins the two MCP tools and, with
  `TestWriteVerbSensorFailsOnWriteNames`, that no tool name is a write verb; `TestEveryToolIsReadOnlyAnnotatedL1` pins
  the annotations and authority. There is no golden registry file.
- `internal/adapters/kafka/cloudevents/cloudevents_test.go` and `internal/adapters/outbound/kafka/encoder_test.go`
  pin the CloudEvents envelope.
- CI `api-lint` runs Spectral on `apis/openapi.yaml` (`.spectral.yaml`) and `apis/asyncapi.yaml`
  (`.spectral.asyncapi.yaml`) with `--fail-severity=warn`.

## Mutation testing

`.gremlins.yaml` sets one worker, timeout coefficient 30, and thresholds **efficacy 99** and **mutant-coverage 97**.
The file records the last measurement (2026-11-15): `network` and `shared` at 100 % / 100 %; `returnauthorization` 88
killed, 0 lived, 2 not covered (efficacy 100 %, mutator coverage 97.78 %). `make mutation` runs gremlins on
`internal/domain/returnauthorization` (the fast subset); `make mutation-full` on all of `internal/domain`; the CI
`mutation-fast` job loops over every package under `internal/domain`.
gremlins is pinned to v0.6.0.

## Architecture fitness tests

`make arch-test` runs `go test ./internal/architecture/... -v`. Each rule has a "sensor bites" companion that proves the
rule fails on a bad fixture.

| Rule | Test |
| --- | --- |
| Hexagonal dependency direction (`domain` imports nothing outward, `application` only domain, adapters do not import each other) | `TestHexagonalArchitecture` |
| MCP and A2A adapters are read-only and import no REST adapter or outbound client | `TestMCPAdapterDependencyRule`, `TestA2AAdapterDependencyRule`, `TestA2AAgentIsReadOnlyAndHasNoOutboundClient` |
| No authentication middleware reintroduced | `TestNoAuthMiddlewareReintroduced` |
| Runtime binaries never import the in-memory adapters | `TestRuntimeBinaryHasNoInMemoryAdapters` |
| No Go file at the repository root | `TestNoGoFilesAtRepoRoot` |
| CloudEvents only, no flat envelope or toggle; replay consumers set a commit interval | `TestNoEventEnvelopeToggleOrFlatEnvelope`, `TestCloudEventsOnly`, `TestReplayConsumersSetCommitInterval` |
| Event catalogue test (every own `com.warehouse.(wms\|wes)` or `com.retailnetwork` type in `apis/asyncapi.yaml` is in ADR 0004) | `TestEventCatalogueMatchesContract` |
| Consumer group ids come from configuration, never a literal | `TestKafkaConsumerGroupNeverHardcodedInline` |
| Kafka writers set `RequiredAcks`; a writer and its message never both set a topic; dead-letter calls guard cancellation | `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterTopicNotSetTwice`, `TestDeadLetterCallsGuardCancellation` |
| Every outbox relay passes the attempt cap | `TestOutboxRelaysPassAttemptCap` |
| Every MCP tool and A2A skill declares an authority level; write tools are at least L3 | `TestEveryMCPToolDeclaresAuthority`, `TestA2ASkillsDeclareAuthority`, `TestMCPWriteToolsAreAtLeastL3` |
| No PII-shaped identifier anywhere; `reason` stays an enum | `TestNoPIIShapedIdentifiersAnywhere`, `TestReasonStaysAnEnum` |
| The analytics read side is self-contained | `TestAnalyticsReadSideArchitecture` |
| MCP optional inputs carry `omitempty` | `TestMCPOptionalToolInputsCarryOmitempty` |
| Integration tests use testcontainers | `TestPostgresIntegrationTestsUseTestcontainers`, `TestKafkaIntegrationTestsUseTestcontainers` |

`modulePath` in `internal/architecture/architecture_test.go` equals the `module` line of `go.mod`
(`github.com/IQVO/customer-returns`), as the arch-go rules require.

## Make targets

| Target | What it runs |
| --- | --- |
| `make build` / `vet` / `fmt` / `fmt-check` | `go build ./...`, `go vet ./...`, `gofmt -w .`, the gofmt check |
| `make lint` | `golangci-lint run --allow-parallel-runners ./...` (pinned v2.14.0) |
| `make test` | `go test ./... -race` |
| `make coverage` | tests plus the 90 % gate |
| `make bdd` | `go test ./... -run TestFeatures -v` |
| `make arch-test` | the fitness tests |
| `make integration` | testcontainers suite (Docker) |
| `make mutation` / `make mutation-full` | gremlins |
| `make vuln` | `govulncheck ./...` |
| `make check` | `fmt-check vet build lint test`; the lefthook pre-push hook runs it |
| `make check-all` | `check` plus `coverage arch-test bdd` |
| `make check-fast`, `make guide-lint`, `make harness-test` | agent-harness helpers (changed packages, guide linter, hook tests) |

## CI jobs (`.github/workflows/ci.yml`)

Triggers: push and pull request on `main` and `develop`, a weekly Monday schedule, and manual dispatch.

| Job | Runs | Gate |
| --- | --- | --- |
| `lint` | golangci-lint v2.14.0 | blocking |
| `guide-lint` | agent-guide sensor and hook tests | advisory (`continue-on-error`) |
| `complexity` | gocyclo, gocognit, cyclop, funlen, nestif plus an informational gocyclo report | blocking |
| `file-length` | revive `file-length-limit` (at most 300 code lines per production Go file, `.golangci.yml`) | blocking on pull requests into `develop` for the Go files the PR adds or touches; an informational report of the whole backlog on push, schedule and release PRs |
| `test` | build, vet, `make coverage` | blocking |
| `bdd` | `go test ./... -run TestFeatures -v` | blocking |
| `integration` | `go test -tags=integration ./... -race -count=1` | blocking |
| `mutation-fast` | gremlins on every package under `internal/domain` | blocking |
| `mutation`, `drift` | exhaustive mutation, deadcode, `go mod tidy -diff`, coverage-quality | scheduled and manual only; open a `harness:red` issue on a scheduled failure |
| `api-lint` | Spectral on both specs | blocking |
| `vuln` | govulncheck | blocking |
| `helm-lint` | `ct lint` and the Service selector test | pull requests only |
| `chart-tests` | every `charts/customer-returns/tests/*.sh` and `test_*.py` | blocking |
| `arch-test` | fitness tests | blocking |
| `pinned-contracts` | the consumer contract tests against the producers' pinned specs (`contracts/pinned.json`) | blocking |
| `docker-publish`, `release` | image, cosign signature, SBOM, Helm chart, tag | push to `main` only, after the jobs above |

`docs.yml` builds this site on pull requests and deploys it on a push to `main`. The CI has no trivy scan and no
generated-docs drift job.
