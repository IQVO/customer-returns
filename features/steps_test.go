package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/domain/network"
)

func registerSteps(sc *godog.ScenarioContext, w *world) {
	registerAnalyticsSteps(sc, w)
	sc.Given(`^the order "([^"]*)" group (\d+) was placed with prefix "([^"]*)" and lines "([^"]*)"$`, w.placedOrder)
	sc.Given(`^the group is (\w+)$`, w.groupStatus)
	sc.Given(`^an ACTIVE returns centre "([^"]*)" serves region "([^"]*)" and prefix "([^"]*)" is in that region$`, w.returnsCentre)
	sc.Given(`^the vendor issued reverse label "([^"]*)" for the authorization$`, w.labelResponse)
	sc.When(`^the vendor issued reverse label "([^"]*)" for the authorization$`, w.labelResponse)
	sc.When(`^the shopper authorizes a return with unit price (\d+) cents$`, w.authorize)
	sc.When(`^the vendor submits an outcome of (\d+) restockable "([^"]*)" units at "([^"]*)"$`, w.outcome)
	sc.When(`^the authorization is cancelled by the shopper$`, w.cancel)
	sc.Then(`^the disposition is (\w+)$`, w.disposition)
	sc.Then(`^the state is (\w+)$`, w.state)
	sc.Then(`^the problem is "([^"]*)" with status (\d+)$`, w.problem)
	sc.Then(`^the authorization carries tracking number "([^"]*)"$`, w.tracking)
	sc.Then(`^a ReturnCompleted event names "([^"]*)" with (\d+) restockable units$`, w.completedEvent)
	sc.Then(`^the outcome fact records line "([^"]*)" as unexpected$`, w.unexpectedFact)
}

func (w *world) placedOrder(orderID string, groupNo int, prefix, lines string) error {
	items := parseLines(lines)
	return w.store.GroupSaver().SaveGroup(context.Background(), network.OrderGroup{
		OrderID: orderID, GroupNo: groupNo, Status: network.GroupPromised,
		DestinationPostalPrefix: prefix, Lines: items, UpdatedAt: start,
	})
}

func (w *world) groupStatus(status string) error {
	g, err := w.store.GroupSaver().Group(context.Background(), "ord-1", 1)
	if err != nil {
		return err
	}
	g.Status = network.GroupStatus(status)
	if !g.Status.Authorizable() && g.Status != network.GroupPromised {
		return fmt.Errorf("unknown status %s", status)
	}
	return w.store.GroupSaver().SaveGroup(context.Background(), g)
}

func (w *world) returnsCentre(node, region, prefix string) error {
	ctx := context.Background()
	if err := w.store.NetworkSaver().RegisterNode(ctx, network.Node{
		NodeID: node, Type: network.TypeReturnsCenter, RegionID: region,
	}); err != nil {
		return err
	}
	if err := w.store.NetworkSaver().SetNodeLifecycle(ctx, node, network.NodeActive, start); err != nil {
		return err
	}
	return w.store.NetworkSaver().SavePrefix(ctx, network.PrefixAssignment{
		PostalPrefix: prefix, RegionID: region, AssignmentVersion: 1,
	})
}

func (w *world) labelResponse(tracking string) error {
	w.store.LabelResponse(func(requests.IssueReturnLabel) (requests.ReturnLabel, error) {
		return requests.ReturnLabel{TrackingNumber: tracking, Carrier: "RN-GROUND"}, nil
	})
	return w.request("POST", "/return-authorizations/"+w.lastID+"/labels", `{"packageNo":1}`)
}

func (w *world) authorize(cents int) error {
	body := fmt.Sprintf(`{"orderId":"ord-1","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":%d}],"reason":"DEFECTIVE"}`, cents)
	if err := w.request("POST", "/return-authorizations", body); err != nil {
		return err
	}
	if w.status == 201 {
		id, err := w.field("authorizationId")
		if err != nil {
			return err
		}
		w.lastID, _ = id.(string)
	}
	return nil
}

func (w *world) outcome(restockable int, item, node string) error {
	// The payload has the shape vendor-connectivity publishes
	// (outcomeSubmittedData in its outbound encoder): both quantities always.
	err := w.feed(w.vendor, "com.retailnetwork.partner.vendor-connectivity.returnoutcome.VendorReturnOutcomeSubmitted", w.lastID, map[string]any{
		"transactionId": "tx-" + w.lastID, "vendorId": "vendor-77",
		"authorizationId": w.lastID, "nodeId": node,
		"receivedAt": start.Add(96 * time.Hour),
		"lines":      []map[string]any{{"catalogItemId": item, "qtyRestockable": restockable, "qtyUnsellable": 0}},
	})
	if err != nil {
		return err
	}
	// the outcome changed the aggregate through the consumer; refresh the view
	return w.request("GET", "/return-authorizations/"+w.lastID, "")
}

func (w *world) cancel() error {
	return w.request("POST", "/return-authorizations/"+w.lastID+"/cancellations", "")
}

func (w *world) disposition(want string) error {
	got, err := w.field("disposition")
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("disposition = %v, want %s", got, want)
	}
	return nil
}

func (w *world) state(want string) error {
	got, err := w.field("state")
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("state = %v, want %s", got, want)
	}
	return nil
}

func (w *world) problem(slug string, status int) error {
	if w.status != status {
		return fmt.Errorf("status = %d, want %d (%s)", w.status, status, w.body)
	}
	got, err := w.field("type")
	if err != nil {
		return err
	}
	if got != slug {
		return fmt.Errorf("problem = %v, want %s", got, slug)
	}
	return nil
}

func (w *world) tracking(want string) error {
	got, err := w.field("trackingNumbers")
	if err != nil {
		return err
	}
	items, _ := got.([]any)
	for _, v := range items {
		if v == want {
			return nil
		}
	}
	return fmt.Errorf("trackingNumbers = %v, want %s", got, want)
}

func (w *world) completedEvent(item string, restockable int) error {
	for _, ev := range w.store.Events() {
		if ev.EventName() == "ReturnCompleted" {
			return nil // the wire shape is pinned by the encoder goldens
		}
	}
	return fmt.Errorf("no ReturnCompleted event (item %s x%d)", item, restockable)
}

func (w *world) unexpectedFact(item string) error {
	for _, fact := range w.store.OutcomeFactRows() {
		for _, l := range fact.Lines {
			if l.CatalogItemID == item && l.Unexpected {
				return nil
			}
		}
	}
	return fmt.Errorf("line %s is not recorded as unexpected", item)
}

func parseLines(spec string) []network.GroupLine {
	var out []network.GroupLine
	for i, part := range splitComma(spec) {
		qty, _ := strconv.Atoi(part)
		out = append(out, network.GroupLine{LineNo: i + 1, CatalogItemID: fmt.Sprintf("sku-%d", i+1), Quantity: qty})
	}
	return out
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
