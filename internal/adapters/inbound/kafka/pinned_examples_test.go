package kafka_test

// The producers' own message examples, read from the pinned specs when the
// test binary starts (contracts/<producer>.asyncapi.yaml,
// components.messages.<Message>.examples). They are the payloads the consumers
// of this context must read; a shape this context invents proves nothing about
// the wire. Nothing is pasted here: refreshing a pin (copy the producer's file
// over it, never edit it) refreshes every example below, and an example its
// producer renamed or dropped stops the package's tests with its name.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/adapters/inbound/kafka"
)

// The four producers this context consumes, by the name of their pin.
const (
	pinCustomerOrdering   = "customer-ordering"
	pinNodeTopology       = "node-topology"
	pinOrderSourcing      = "order-sourcing"
	pinVendorConnectivity = "vendor-connectivity"
)

var (
	exampleCustomerOrderingCustomerOrderPlaced  = mustPinnedExample(pinCustomerOrdering, "CustomerOrderPlaced", "customerOrderPlaced")
	exampleCustomerOrderingShopperNoticeDecided = mustPinnedExample(pinCustomerOrdering, "ShopperNoticeDecided", "shopperNoticeDecided")

	exampleNodeTopologyNodeRegistered                    = mustPinnedExample(pinNodeTopology, "NodeRegistered", "nodeRegistered")
	exampleNodeTopologyNodeActivated                     = mustPinnedExample(pinNodeTopology, "NodeActivated", "nodeActivated")
	exampleNodeTopologyNodeSuspended                     = mustPinnedExample(pinNodeTopology, "NodeSuspended", "nodeSuspended")
	exampleNodeTopologyNodeRetired                       = mustPinnedExample(pinNodeTopology, "NodeRetired", "nodeRetired")
	exampleNodeTopologyServiceAreaAssigned               = mustPinnedExample(pinNodeTopology, "ServiceAreaAssigned", "serviceAreaAssigned")
	exampleNodeTopologyServiceAreaAssignedCarrierHandoff = mustPinnedExample(pinNodeTopology, "ServiceAreaAssigned", "serviceAreaAssignedCarrierHandoff")

	exampleOrderSourcingFulfillmentReleasedToNode       = mustPinnedExample(pinOrderSourcing, "FulfillmentReleasedToNode", "FulfillmentReleasedToNode")
	exampleOrderSourcingFulfillmentReleasedToNodeMerged = mustPinnedExample(pinOrderSourcing, "FulfillmentReleasedToNode", "FulfillmentReleasedToNodeMerged")
	exampleOrderSourcingFulfillmentLocked               = mustPinnedExample(pinOrderSourcing, "FulfillmentLocked", "FulfillmentLocked")

	exampleVendorConnectivityVendorReturnOutcomeSubmitted = mustPinnedExample(pinVendorConnectivity, "VendorReturnOutcomeSubmitted", "vendorReturnOutcomeSubmitted")
)

// mustPinnedExample returns the named example of a message of a pinned spec
// as the JSON the consumer receives.
func mustPinnedExample(producer, message, example string) string {
	spec, err := readPinnedAsyncAPI(producer)
	if err != nil {
		panic(err)
	}
	body := asMap(asMap(asMap(spec.root["components"])["messages"])[message])
	for _, ex := range (pinnedMessage{name: message, body: body}).examples() {
		if ex.name != example {
			continue
		}
		wire, err := json.Marshal(ex.event)
		if err != nil {
			panic(fmt.Errorf("the pinned %s example %s/%s does not render as JSON: %w", producer, message, example, err))
		}
		return string(wire)
	}
	panic(fmt.Sprintf("the pinned %s spec has no example %q of message %s (renamed or dropped by the producer?)", producer, example, message))
}

// feedExample hands the producer's example to the consumer that owns its
// topic and fails when the consumer has no handler for the example's type or
// cannot apply it.
func feedExample(t *testing.T, c *kafka.Consumer, example string) {
	t.Helper()
	typ := exampleAttr(t, example, "type")
	if _, ok := c.Handlers[typ]; !ok {
		t.Fatalf("the %s consumer has no handler for the producer's type %q", c.Name, typ)
	}
	if err := c.HandleMessage(context.Background(), []byte(example)); err != nil {
		t.Fatalf("the producer's own example of %s is not applied: %v", typ, err)
	}
}

// exampleEnvelope decodes an example into its CloudEvents attributes.
func exampleEnvelope(t *testing.T, example string) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal([]byte(example), &envelope); err != nil {
		t.Fatalf("producer example: %v", err)
	}
	return envelope
}

// exampleAttr reads one CloudEvents attribute of an example; it must be set.
func exampleAttr(t *testing.T, example, attribute string) string {
	t.Helper()
	return text(t, exampleEnvelope(t, example), attribute)
}

// exampleData is the `data` of an example.
func exampleData(t *testing.T, example string) map[string]any {
	t.Helper()
	return asMap(exampleEnvelope(t, example)["data"])
}

// exampleField reads one string of an example's `data`; it must be set.
func exampleField(t *testing.T, example, field string) string {
	t.Helper()
	return text(t, exampleData(t, example), field)
}

// exampleTime is the CloudEvents `time` of an example.
func exampleTime(t *testing.T, example string) time.Time {
	t.Helper()
	return instant(t, exampleAttr(t, example, "time"))
}
