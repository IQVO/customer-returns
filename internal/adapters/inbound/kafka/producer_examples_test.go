package kafka_test

// The producers' own message examples, copied from each producer's
// apis/asyncapi.yaml on origin/develop (components.messages.<Message>.examples)
// and rendered as JSON: every key and value is the producer's, in the
// producer's order. They are the payloads the consumers of this context must
// read; a shape this context invents proves nothing about the wire. Refresh
// them from the producer when its contract changes, never by hand.

// exampleCustomerOrderingCustomerOrderPlaced is IQVO/customer-ordering apis/asyncapi.yaml at c817ee2, message CustomerOrderPlaced,
// example "customerOrderPlaced".
const exampleCustomerOrderingCustomerOrderPlaced = `{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40",
  "source": "/retailnetwork/customer-ordering",
  "type": "com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced",
  "subject": "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10",
  "time": "2026-10-23T10:15:30Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:customer-ordering:events:CustomerOrderPlaced:v1",
  "data": {
    "orderId": "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10",
    "shopperId": "3b0c1f0e-6f0a-4a39-8a55-2b7f0d8c9e11",
    "destinationPostalPrefix": "787",
    "placedAt": "2026-10-23T10:15:30Z",
    "consolidationKey": "9f2c6d0b1a7e4c3d8b5a0e6f1d2c3b4a5968778695a4b3c2d1e0f9a8b7c6d5e4",
    "groups": [
      {
        "groupNo": 1,
        "promiseId": "p-1001",
        "tier": "NEXT_DAY",
        "witnessNodeId": "fc-aus-1",
        "shipBy": "2026-10-23T22:00:00Z",
        "deliverByEarliest": "2026-10-24T13:00:00Z",
        "deliverByLatest": "2026-10-24T22:00:00Z",
        "timeZone": "America/Chicago",
        "lines": [
          {
            "lineNo": 1,
            "catalogItemId": "sku-100",
            "quantity": 2
          }
        ]
      }
    ]
  }
}`

// exampleCustomerOrderingShopperNoticeDecided is IQVO/customer-ordering apis/asyncapi.yaml at c817ee2, message ShopperNoticeDecided,
// example "shopperNoticeDecided".
const exampleCustomerOrderingShopperNoticeDecided = `{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40",
  "source": "/retailnetwork/customer-ordering",
  "type": "com.retailnetwork.commerce.customer-ordering.order.ShopperNoticeDecided",
  "subject": "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10",
  "time": "2026-10-23T12:15:30Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:customer-ordering:events:ShopperNoticeDecided:v1",
  "data": {
    "orderId": "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10",
    "groupNo": 2,
    "family": "ETA_WINDOW_ASSIGNED",
    "notifier": "NOBODY",
    "policyVersion": 0,
    "noticeKey": "e17a743b013e6726bd9b0dcb238ce8a8909bc9e18237878d103b54b7edba7e76",
    "decidedAt": "2026-10-23T12:15:30Z",
    "fact": {
      "etaStart": "2026-10-24T16:15:30Z",
      "etaEnd": "2026-10-24T17:15:30Z"
    }
  }
}`

// exampleNodeTopologyNodeRegistered is IQVO/node-topology apis/asyncapi.yaml at d3e6211, message NodeRegistered,
// example "nodeRegistered".
const exampleNodeTopologyNodeRegistered = `{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40",
  "source": "/retailnetwork/node-topology",
  "type": "com.retailnetwork.logistics.node-topology.node.NodeRegistered",
  "subject": "node-0f3a91",
  "time": "2026-10-22T13:00:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:node-topology:events:NodeRegistered:v1",
  "data": {
    "nodeId": "node-0f3a91",
    "nodeType": "FULFILLMENT_CENTER",
    "subtype": "SORTABLE",
    "operator": "VENDOR",
    "vendorId": "vendor-77",
    "vendorNodeCode": "SP-CAJAMAR-01",
    "regionId": "region-se",
    "timezone": "America/Sao_Paulo",
    "postalCode": "07750-000",
    "countryCode": "BR"
  }
}`

// exampleNodeTopologyNodeActivated is IQVO/node-topology apis/asyncapi.yaml at d3e6211, message NodeActivated,
// example "nodeActivated".
const exampleNodeTopologyNodeActivated = `{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c45",
  "source": "/retailnetwork/node-topology",
  "type": "com.retailnetwork.logistics.node-topology.node.NodeActivated",
  "subject": "node-0f3a91",
  "time": "2026-10-22T14:00:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:node-topology:events:NodeActivated:v1",
  "data": {
    "nodeId": "node-0f3a91",
    "activatedAt": "2026-10-22T14:00:00Z"
  }
}`

// exampleNodeTopologyNodeSuspended is IQVO/node-topology apis/asyncapi.yaml at d3e6211, message NodeSuspended,
// example "nodeSuspended".
const exampleNodeTopologyNodeSuspended = `{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c46",
  "source": "/retailnetwork/node-topology",
  "type": "com.retailnetwork.logistics.node-topology.node.NodeSuspended",
  "subject": "node-0f3a91",
  "time": "2026-10-23T08:30:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:node-topology:events:NodeSuspended:v1",
  "data": {
    "nodeId": "node-0f3a91",
    "reason": "carrier strike at the dock",
    "suspendedAt": "2026-10-23T08:30:00Z"
  }
}`

// exampleNodeTopologyNodeRetired is IQVO/node-topology apis/asyncapi.yaml at d3e6211, message NodeRetired,
// example "nodeRetired".
const exampleNodeTopologyNodeRetired = `{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c47",
  "source": "/retailnetwork/node-topology",
  "type": "com.retailnetwork.logistics.node-topology.node.NodeRetired",
  "subject": "node-0f3a91",
  "time": "2026-11-30T23:59:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:node-topology:events:NodeRetired:v1",
  "data": {
    "nodeId": "node-0f3a91",
    "retiredAt": "2026-11-30T23:59:00Z"
  }
}`

// exampleNodeTopologyServiceAreaAssigned is IQVO/node-topology apis/asyncapi.yaml at d3e6211, message ServiceAreaAssigned,
// example "serviceAreaAssigned".
const exampleNodeTopologyServiceAreaAssigned = `{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c4a",
  "source": "/retailnetwork/node-topology",
  "type": "com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned",
  "subject": "01310",
  "time": "2026-10-22T16:00:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:node-topology:events:ServiceAreaAssigned:v1",
  "data": {
    "postalPrefix": "01310",
    "deliveryStationNodeId": "node-9b7e02",
    "regionId": "region-se",
    "assignmentVersion": 2
  }
}`

// exampleNodeTopologyServiceAreaAssignedCarrierHandoff is IQVO/node-topology apis/asyncapi.yaml at d3e6211, message ServiceAreaAssigned,
// example "serviceAreaAssignedCarrierHandoff".
const exampleNodeTopologyServiceAreaAssignedCarrierHandoff = `{
  "specversion": "1.0",
  "id": "7a1c4e8b-2d4f-4a63-9e55-1b0c9a4e5f21",
  "source": "/retailnetwork/node-topology",
  "type": "com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned",
  "subject": "01310",
  "time": "2026-11-13T09:00:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:node-topology:events:ServiceAreaAssigned:v1",
  "data": {
    "postalPrefix": "01310",
    "deliveryStationNodeId": "node-9b7e02",
    "regionId": "region-se",
    "assignmentVersion": 3,
    "deliveryMode": "CARRIER_HANDOFF",
    "carriers": [
      {
        "carrierId": "carrier-a",
        "handoffNodeId": "node-5c21d8",
        "costPerPackageCents": 320,
        "transitMinutes": 95
      },
      {
        "carrierId": "carrier-b",
        "handoffNodeId": "node-9b7e02",
        "costPerPackageCents": 400,
        "transitMinutes": 60
      }
    ]
  }
}`

// exampleOrderSourcingFulfillmentReleasedToNode is IQVO/order-sourcing apis/asyncapi.yaml at f352218, message FulfillmentReleasedToNode,
// example "FulfillmentReleasedToNode".
const exampleOrderSourcingFulfillmentReleasedToNode = `{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c66",
  "source": "/retailnetwork/order-sourcing",
  "type": "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode",
  "subject": "asg-7f3c2d10",
  "time": "2026-10-22T19:01:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:order-sourcing:events:FulfillmentReleasedToNode:v1",
  "data": {
    "assignmentId": "asg-7f3c2d10",
    "releaseNo": 1,
    "orderId": "ord-51a0c9",
    "groupNo": 1,
    "nodeId": "node-fc-ws-sor-01",
    "pickupDepartureId": "dep-pk-20261022-1800-fc-ws-sor-01",
    "requiredShipBy": "2026-10-22T21:00:00Z",
    "promisedDeliverByLatest": "2026-10-24T23:00:00Z",
    "tier": "TWO_DAY",
    "estimatedCostCents": 1065,
    "policyVersion": 2,
    "effectiveCostCents": 1315,
    "estimatedCo2eGrams": 760,
    "emissionComplete": true,
    "lines": [
      {
        "catalogItemId": "cat-000123",
        "listingId": "lst-000123-fc-ws-sor-01",
        "quantity": 2
      },
      {
        "catalogItemId": "cat-000456",
        "listingId": "lst-000456-fc-ws-sor-01-sel-0042",
        "ownerRef": "sel-0042",
        "quantity": 1
      }
    ]
  }
}`

// exampleOrderSourcingFulfillmentReleasedToNodeMerged is IQVO/order-sourcing apis/asyncapi.yaml at f352218, message FulfillmentReleasedToNode,
// example "FulfillmentReleasedToNodeMerged".
const exampleOrderSourcingFulfillmentReleasedToNodeMerged = `{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c67",
  "source": "/retailnetwork/order-sourcing",
  "type": "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode",
  "subject": "asg-7f3c2d10",
  "time": "2026-10-22T19:01:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:order-sourcing:events:FulfillmentReleasedToNode:v1",
  "data": {
    "assignmentId": "asg-7f3c2d10",
    "releaseNo": 1,
    "orderId": "ord-51a0c9",
    "groupNo": 1,
    "nodeId": "node-fc-ws-sor-01",
    "pickupDepartureId": "dep-pk-20261022-1800-fc-ws-sor-01",
    "requiredShipBy": "2026-10-22T21:00:00Z",
    "promisedDeliverByLatest": "2026-10-24T23:00:00Z",
    "tier": "STANDARD",
    "lines": [
      {
        "catalogItemId": "cat-000123",
        "listingId": "lst-000123-fc-ws-sor-01",
        "quantity": 2,
        "assignmentId": "asg-7f3c2d10"
      },
      {
        "catalogItemId": "cat-000123",
        "listingId": "lst-000123-fc-ws-sor-01",
        "quantity": 3,
        "assignmentId": "asg-2c9d41aa"
      }
    ],
    "members": [
      {
        "assignmentId": "asg-7f3c2d10",
        "orderId": "ord-51a0c9",
        "groupNo": 1,
        "promiseId": "prm-88c1"
      },
      {
        "assignmentId": "asg-2c9d41aa",
        "orderId": "ord-6b2e07",
        "groupNo": 1,
        "promiseId": "prm-91d4"
      }
    ]
  }
}`

// exampleOrderSourcingFulfillmentLocked is IQVO/order-sourcing apis/asyncapi.yaml at f352218, message FulfillmentLocked,
// example "FulfillmentLocked".
const exampleOrderSourcingFulfillmentLocked = `{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40",
  "source": "/retailnetwork/order-sourcing",
  "type": "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked",
  "subject": "asg-7f3c2d10",
  "time": "2026-10-22T19:01:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:order-sourcing:events:FulfillmentLocked:v1",
  "data": {
    "assignmentId": "asg-7f3c2d10",
    "orderId": "ord-51a0c9",
    "groupNo": 1,
    "promiseId": "prm-88c1",
    "nodeId": "node-fc-ws-sor-01",
    "pickupDepartureId": "dep-pk-20261022-1800-fc-ws-sor-01",
    "lockedAt": "2026-10-22T19:01:00Z",
    "lockReason": "PURCHASE_ORDER_ACCEPTED"
  }
}`

// exampleOrderSourcingFulfillmentLockedFollower is IQVO/order-sourcing apis/asyncapi.yaml at f352218, message FulfillmentLocked,
// example "FulfillmentLockedFollower".
const exampleOrderSourcingFulfillmentLockedFollower = `{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c41",
  "source": "/retailnetwork/order-sourcing",
  "type": "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked",
  "subject": "asg-2c9d41aa",
  "time": "2026-10-22T19:01:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:order-sourcing:events:FulfillmentLocked:v1",
  "data": {
    "assignmentId": "asg-2c9d41aa",
    "orderId": "ord-6b2e07",
    "groupNo": 1,
    "promiseId": "prm-91d4",
    "nodeId": "node-fc-ws-sor-01",
    "pickupDepartureId": "dep-pk-20261022-1800-fc-ws-sor-01",
    "lockedAt": "2026-10-22T19:01:00Z",
    "lockReason": "PURCHASE_ORDER_ACCEPTED",
    "consolidatedInto": "asg-7f3c2d10",
    "mergedAssignmentIds": [
      "asg-2c9d41aa",
      "asg-7f3c2d10"
    ]
  }
}`

// exampleVendorConnectivityVendorReturnOutcomeSubmitted is IQVO/vendor-connectivity apis/asyncapi.yaml at 0f6f54d, message VendorReturnOutcomeSubmitted,
// example "vendorReturnOutcomeSubmitted".
const exampleVendorConnectivityVendorReturnOutcomeSubmitted = `{
  "specversion": "1.0",
  "id": "5b2f8d63-1c0d-4a47-b9d2-7f3cae4a6b81",
  "source": "/retailnetwork/vendor-connectivity",
  "type": "com.retailnetwork.partner.vendor-connectivity.returnoutcome.VendorReturnOutcomeSubmitted",
  "subject": "ret-01J9Z6K4M2",
  "time": "2026-11-14T13:00:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:vendor-connectivity:events:VendorReturnOutcomeSubmitted:v1",
  "data": {
    "transactionId": "tx-01J9Z6K4M2",
    "vendorId": "V-WS",
    "authorizationId": "ret-01J9Z6K4M2",
    "nodeId": "n-1",
    "receivedAt": "2026-11-14T12:30:00Z",
    "lines": [
      {
        "catalogItemId": "ci-100",
        "qtyRestockable": 2,
        "qtyUnsellable": 1
      },
      {
        "catalogItemId": "ci-100",
        "ownerRef": "seller-7",
        "qtyRestockable": 0,
        "qtyUnsellable": 3
      }
    ]
  }
}`
