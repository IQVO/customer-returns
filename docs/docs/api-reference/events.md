---
id: events
title: Event catalogue
sidebar_label: Events
sidebar_position: 2
description: "The CloudEvents customer-returns publishes and consumes: types, topics, keys, trigger, consumers and delivery rules."
---

# Event catalogue

The contract is `apis/asyncapi.yaml` (AsyncAPI 3.0.0). Every message is CloudEvents 1.0 in structured mode, built and
decoded only in `internal/adapters/kafka/cloudevents`. Field-level payloads are on [Domain events](/docs/ddd/domain-events).

## Envelope

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` |
| `id` | a UUID minted once, when the event is encoded into the outbox; a relay retry republishes the same id |
| `source` | `/retailnetwork/customer-returns` |
| `type` | `com.retailnetwork.commerce.customer-returns.returnauthorization.<EventName>`; the single entity is `returnauthorization` |
| `subject` | the aggregate root id: the `authorizationId` |
| `time` | the instant the fact became true |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:customer-returns:<stream>:<EventName>:v<version>`, stream `events`; the version is the aggregate version after the change |
| Kafka key | the `subject`, so one authorization's events stay ordered on one partition |
| Kafka header | `content-type: application/cloudevents+json; charset=UTF-8` |

Source: `internal/adapters/kafka/cloudevents/cloudevents.go`, `internal/adapters/outbound/kafka/encoder.go`.

## Published

Each event is appended to `outbox_events` in the same Postgres transaction as the change that raised it, then relayed by
the in-process outbox relay. Delivery is at-least-once; consumers dedupe on `id`.

| Event | Full `type` | Topic | Raised by | Consumers |
| --- | --- | --- | --- | --- |
| `ReturnAuthorized` | `com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnAuthorized` | `retailnetwork.customer-returns.events` | `AuthorizeReturn` (every disposition, KEEP included) | vendor-connectivity (opens the return advice) |
| `ReturnLabelIssued` | `com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnLabelIssued` | `retailnetwork.customer-returns.events` | `IssueLabel`, once per package | vendor-connectivity |
| `ReturnCompleted` | `com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnCompleted` | `retailnetwork.customer-returns.events` | `OnOutcomeSubmitted`, the first fitting graded outcome | offer-availability (restocks listings), vendor-connectivity |
| `ReturnCancelled` | `com.retailnetwork.commerce.customer-returns.returnauthorization.ReturnCancelled` | `retailnetwork.customer-returns.events` | `CancelReturn` (`SHOPPER_REQUESTED`), the lazy expiry (`EXPIRED`) | vendor-connectivity |

All four types go to the one integration topic (`buildMessage` in `encoder.go`); the analytics topic
`retailnetwork.customer-returns.analytics` is reserved but carries no event in v1. Reads, `MarkInTransit` and unexpected
outcome lines publish nothing: a read raises no fact, transit is observed rather than commanded, and unexpected lines
stay recorded facts ([ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue)).

## Consumed

One consumer per topic, each under a mandatory consumer group (`ORDER_CONSUMER_GROUP`, `TOPOLOGY_CONSUMER_GROUP`,
`SOURCING_CONSUMER_GROUP`, `VENDOR_CONSUMER_GROUP`); dispatch is on the full `type`; effects are idempotent on the
CloudEvents `id` (`processed_events`).

| Producer | Topic | Full `type` | Effect |
| --- | --- | --- | --- |
| customer-ordering | `retailnetwork.customer-ordering.events` | `com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced` | Writes each group's lines and witness and the ORDER's `destinationPostalPrefix` (published once per order), `PROMISED`; never demotes a status a notice already set. |
| customer-ordering | `retailnetwork.customer-ordering.events` | `com.retailnetwork.commerce.customer-ordering.order.ShopperNoticeDecided` | Sets the status (a terminal family fact wins); creates the row for an unknown group. |
| node-topology | `retailnetwork.node-topology.events` | `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | Writes the node's type and region in `returns_network_nodes`; a new node starts PENDING, a node already held keeps its lifecycle. |
| node-topology | `retailnetwork.node-topology.events` | `com.retailnetwork.logistics.node-topology.node.NodeActivated` | Lifecycle `ACTIVE` — the `Decide` candidate gate. Carries the node id and an instant only: nothing but the lifecycle moves. |
| node-topology | `retailnetwork.node-topology.events` | `com.retailnetwork.logistics.node-topology.node.NodeSuspended` | Lifecycle `SUSPENDED` (node id and instant only). |
| node-topology | `retailnetwork.node-topology.events` | `com.retailnetwork.logistics.node-topology.node.NodeRetired` | Lifecycle `RETIRED`, terminal (node id and instant only). |
| node-topology | `retailnetwork.node-topology.events` | `com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned` | Upserts the prefix's region; the highest `assignmentVersion` wins. |
| order-sourcing | `retailnetwork.order-sourcing.events` | `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode` | Merges the per-item `ownerRef`s into `group_origins`; a merged release writes one origin per group in `members`. |
| order-sourcing | `retailnetwork.order-sourcing.events` | `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked` | Names the fulfilling node (the ORIGIN_NODE fallback). |
| vendor-connectivity | `retailnetwork.vendor-connectivity.events` | `com.retailnetwork.partner.vendor-connectivity.returnoutcome.VendorReturnOutcomeSubmitted` | Records the graded outcome; the first that fits completes the authorization. |

Failure handling (retry, dead letters, poison messages) is on [Integration](/docs/ecosystem/integration#upstream-what-customer-returns-consumes).

## Rules for changing the catalogue

Per `CLAUDE.md`: a new event is a domain struct, an entry in `apis/asyncapi.yaml` and the `type:` line in
[ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue), in the same commit. The fleet catalogue test
(`TestEventCatalogueMatchesContract`) matches `com.warehouse.(wms|wes)` and `com.retailnetwork` types, so it fails the
build if one of this repository's published `com.retailnetwork.commerce.customer-returns` types is declared in the
AsyncAPI file but missing from ADR 0004. CI `api-lint` runs Spectral on the AsyncAPI file.
