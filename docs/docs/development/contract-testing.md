---
id: contract-testing
title: Consumer contract testing
sidebar_label: Contract testing
sidebar_position: 2
description: "The pinned producer contracts of customer-returns: which specs are pinned, where the pins live and why, the stale-pin gate, the consumer contract tests, and how a pin is refreshed."
---

# Consumer contract testing

customer-returns consumes four producers' events and calls one producer's REST route. Its tests can only prove
that it reads what those producers send if they are fed the producers' own contracts. This page is this
repository's application of the fleet standard (`docs/contract-testing.md` in `IQVO/warehouse-harness-template`).

Why it exists: this context once read a payload shape its producer never published. Every real node status fact was
dead-lettered while the tests stayed green, because the tests fed the consumer the shape it expected
(ADR 0003, addendum of 2026-11-17).

## The two halves

1. **The pins.** A byte copy of each producer spec this context depends on, registered in `contracts/pinned.json`.
   The CI job `pinned-contracts` runs `scripts/harness/pinned_contract_gate.py`, which reads each producer's
   `develop` branch and fails when a pin differs from it by a single byte (exit 1), or when a producer cannot be
   read (exit 2: a gate that cannot look never passes).
2. **The tests.** Consumer contract tests read the pinned files at test time and drive this context's real
   consumers and its real label client with them.

## What is pinned

The edge list comes from the code, and a test keeps it honest: `TestPinnedAsyncAPICoversSubscriptions` fails when
the production code of `internal/adapters/inbound/kafka` names a topic of another context that no pin covers, and
`TestPinnedSpecCoversEveryCallSite` fails when the label client builds a second HTTP request.

| Pin | Producer file | Edge in this repository |
| --- | --- | --- |
| `contracts/customer-ordering.asyncapi.yaml` | `IQVO/customer-ordering` `apis/asyncapi.yaml` | `NewOrderConsumer`, topic `retailnetwork.customer-ordering.events`: `CustomerOrderPlaced`, `ShopperNoticeDecided` |
| `contracts/node-topology.asyncapi.yaml` | `IQVO/node-topology` `apis/asyncapi.yaml` | `NewTopologyConsumer`, topic `retailnetwork.node-topology.events`: `NodeRegistered`, `NodeActivated`, `NodeSuspended`, `NodeRetired`, `ServiceAreaAssigned` |
| `contracts/order-sourcing.asyncapi.yaml` | `IQVO/order-sourcing` `apis/asyncapi.yaml` | `NewSourcingConsumer`, topic `retailnetwork.order-sourcing.events`: `FulfillmentReleasedToNode`, `FulfillmentLocked` |
| `contracts/vendor-connectivity.asyncapi.yaml` | `IQVO/vendor-connectivity` `apis/asyncapi.yaml` | `NewVendorConsumer`, topic `retailnetwork.vendor-connectivity.events`: `VendorReturnOutcomeSubmitted` |
| `contracts/shipping-and-tracking.openapi.yaml` | `IQVO/shipping-and-tracking` `apis/openapi.yaml` | `internal/adapters/outbound/labels/client.go`, `POST /return-labels` (ADR 0005) |

Not pinned, on purpose: the analytics projector (`cmd/projector`, ADR 0012) reads this context's OWN topic
`retailnetwork.customer-returns.events`. Its contract is this repository's `apis/asyncapi.yaml`, already checked by
`TestAnalyticsTypesAreThePublishedOnesOfTheAsyncAPI`; there is no producer to drift from.

The producer commit each pin was copied from is recorded in `contracts/README.md`.

## Where the pins live, and why

The pins are at the repository root, in `contracts/`, beside the registry. The fleet convention is
`internal/adapters/outbound/<producer>/testdata/`; this repository cannot follow it.

`internal/architecture/pii_fitness_test.go` scans every `.go` (except `_test.go`), `.sql`, `.json`, `.feature` and
`.yaml` file under `internal/`, `cmd/` and `features/` for the identifier shapes personal data would take, and its
message says not to loosen it. A producer's spec legitimately contains such identifiers, because the producer owns
the fact: today node-topology's `NodeRegistered` carries the node's `postalCode`, and any producer may add another.
A pin under `internal/**/testdata/` would fail the sensor, and the ways past it are all wrong: exempting a
directory loosens the sensor, and trimming or renaming a pin breaks the byte-copy rule the drift gate rests on.

`contracts/` is outside the sensor's scan roots, and the registry's `local` path is free. The sensor is unchanged
and keeps scanning everything this context owns. This placement is a decision the owner can overrule: the
alternative is an explicit exemption for pinned copies inside the sensor.

## The tests

Event edges, in `internal/adapters/inbound/kafka` (one subtest per producer, one per pinned example):

| Archetype | Test | What it proves |
| --- | --- | --- |
| E1 | `TestPinnedAsyncAPICoversSubscriptions` | The topic each consumer's kafka-go reader is configured with is a channel of its producer's pin, and every type in the consumer's handler table is documented on it. |
| E2 | `TestGoldenEventsDecode` | Every pinned example of every consumed type is applied by the real handler and leaves the state its golden describes (expectations are read from the example). The CloudEvents `id` is claimed in the inbox, and the same `id` delivered again with other values changes nothing. |
| E3 | `TestCloudEventsEnvelopeInvariants` | `specversion` 1.0, `id`, `source`, `type`, `subject`, `time`, `datacontenttype`, `dataschema`: equal to the pinned schema's `const` where it has one, otherwise held to the network's naming with the context, stream and event name read from the pin; the repository's CloudEvents decoder accepts the example. |
| E4 | `TestUnknownAttributesTolerated` | Unknown keys at any depth of `data` and an unknown extension attribute do not change what the example does. |

Beside them: `TestExamplesThatShareAnIDAreOneDeliveryToTheInbox` (a producer that stamps one `id` on all of its
examples: the inbox key is the `id` alone, which is why every golden runs on a state of its own),
`TestThePinParserReadsTheFourAsyncAPIShapes` (the estate's four AsyncAPI layouts, on synthetic documents) and the
scenario tests of `producer_contract_test.go` (sequences, out-of-order facts, dead letters), fed from the pins
through `pinned_examples_test.go`.

REST edge, in `internal/adapters/outbound/labels`:

| Archetype | Test | What it proves |
| --- | --- | --- |
| R1 | `TestPinnedSpecCoversEveryCallSite` | The method and path the real client puts on the wire are an operation of the pin; every header the route requires (path-item and operation parameters) is sent and satisfies its schema, `Idempotency-Key` included; the request body satisfies the pinned request schema. |
| R2 | `TestExamplesSatisfyPinnedSpec` | The answer fixtures satisfy the pinned schema of the 201. |
| R3 | `TestGatewayDecodesSpecExamples` | The real client decodes a spec-shaped 201 and maps every error status the pin documents for the route to `ports.ErrLabelUnavailable`. |
| R4 | `TestFieldsWeReadAreSpecified` | Every field of the client's wire structs is declared by the pin with a compatible type; a property the pin does not require must be one the client tolerates the absence of; the request carries no property the pin deprecates. |

A fixture is taken from the pin's own examples. Where the pin has none, it is derived from the pinned schema's
required properties and reported as "derived; producer example backfill pending". Today that is the whole REST
edge: the shipping-and-tracking spec has no example of `POST /return-labels`.

## Refreshing a pin

When `pinned-contracts` is red, the producer moved. Refresh within two business days:

```bash
gh api -H 'Accept: application/vnd.github.raw' \
  'repos/IQVO/node-topology/contents/apis/asyncapi.yaml?ref=develop' \
  > contracts/node-topology.asyncapi.yaml
GH_TOKEN=$(gh auth token) python3 scripts/harness/pinned_contract_gate.py
go test ./internal/adapters/inbound/kafka/ ./internal/adapters/outbound/labels/
```

1. Copy the producer's file over the pin. Never edit a pin: the gate compares bytes, so a local fix is
   indistinguishable from drift.
2. Run the contract tests and fix what they report, in this repository's code or tests. That is the point of the
   refresh: it is where a producer's change is learned.
3. Update the producer commit in `contracts/README.md` in the same commit.

If a refreshed pin shows a real incompatibility that cannot be fixed here (the producer dropped a field a handler
requires), do not edit the pin: it is a conversation with the producer's owners.

## Open points

- **`pinned-contracts` is advisory.** The job runs on every push and pull request but is not a required status
  check of `develop`; making it one is the owner's decision.
- **The `POST /return-labels` request names the destination node** since shipping-and-tracking's ADR 0018
  (pin refreshed 2026-11-18). The provider still accepts the deprecated `destinationPostalPrefix`; this client
  does not send it, and R4 fails if the client's request carries a property the pin deprecates. When the provider
  removes the deprecated property, the refresh of the pin needs no change here.
- **Unknown `dataschema` versions.** The consumers dispatch on the full `type` and never read `dataschema`, so an
  event of a known type with a version this context has not seen is handled like any other. Whether it should be
  skipped or dead-lettered instead is not decided (ADR 0003 and ADR 0004 do not say).
  `TestAnUnknownDataschemaVersionNeverFailsTheConsumer` asserts only what holds under either decision.
