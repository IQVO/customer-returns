// Package labels is the ONE in-org outbound HTTP client of this context: the
// reverse-label route of the shipping context (ADR 0005). It is config-gated:
// with no base URL configured every call answers ports.ErrLabelUnavailable --
// the authorize-and-link flow stays available and honest (422
// label-unavailable) until the route and its config ship.
package labels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
)

// Client is the ports.LabelIssuer over the shipping context's
// POST /return-labels route.
type Client struct {
	base string
	http *http.Client
}

// NewClient builds the client. An empty base URL keeps the client disabled
// (every call answers ports.ErrLabelUnavailable).
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{base: strings.TrimRight(strings.TrimSpace(baseURL), "/"), http: httpClient}
}

var _ ports.LabelIssuer = (*Client)(nil)

// request is the IssueReturnLabelRequest of the route. The return destination
// is named by its node id, the one of the route's two forms this context
// uses: it never sends the deprecated caller-supplied prefix, and the route
// refuses a request that carries both.
type request struct {
	ReturnAuthorizationID string `json:"returnAuthorizationId"`
	OrderID               string `json:"orderId"`
	PackageNo             int    `json:"packageNo"`
	DestinationNodeID     string `json:"destinationNodeId"`
}

// response is what this context reads of the route's ReturnLabel answer: two
// of its required properties, under the JSON names the shipping context's
// apis/openapi.yaml declares. Everything else the label carries stays there.
type response struct {
	TrackingNumber string `json:"trackingNumber"`
	Carrier        string `json:"carrier"`
}

// issuedStatus is the ONE success of POST /return-labels: the route answers
// 201 Created, and a replay of the same Idempotency-Key answers that first
// response again (201 plus Idempotency-Replayed: true), never 200.
const issuedStatus = http.StatusCreated

// IssueReturnLabel implements ports.LabelIssuer. A disabled client, a route
// that is not live yet, any transport failure and any answer but the 201 with
// a tracking number map to ports.ErrLabelUnavailable: never a 500, never a
// retry storm.
func (c *Client) IssueReturnLabel(ctx context.Context, in requests.IssueReturnLabel) (requests.ReturnLabel, error) {
	if c.base == "" {
		return requests.ReturnLabel{}, ports.ErrLabelUnavailable
	}
	body, err := json.Marshal(request{
		ReturnAuthorizationID: string(in.ReturnAuthorizationID),
		OrderID:               in.OrderID,
		PackageNo:             in.PackageNo,
		DestinationNodeID:     in.DestinationNodeID,
	})
	if err != nil {
		return requests.ReturnLabel{}, ports.ErrLabelUnavailable
	}
	url := c.base + "/return-labels"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return requests.ReturnLabel{}, ports.ErrLabelUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// The shipping context dedupes RETURN labels per (returnAuthorizationId,
	// packageNo); the key restates that pair so a retry is a replay. A refusal
	// for a node the route does not know yet (409
	// destination-prefix-unavailable) is not kept under the key there, so the
	// same key is served once the route knows the node.
	req.Header.Set("Idempotency-Key", fmt.Sprintf("%s:%d", in.ReturnAuthorizationID, in.PackageNo))
	resp, err := c.http.Do(req)
	if err != nil {
		return requests.ReturnLabel{}, ports.ErrLabelUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != issuedStatus {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return requests.ReturnLabel{}, ports.ErrLabelUnavailable
	}
	var out response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return requests.ReturnLabel{}, ports.ErrLabelUnavailable
	}
	if out.TrackingNumber == "" {
		return requests.ReturnLabel{}, ports.ErrLabelUnavailable
	}
	return requests.ReturnLabel{TrackingNumber: out.TrackingNumber, Carrier: out.Carrier}, nil
}

// Disabled is the nil-client stand-in used when no base URL is configured.
var Disabled = disabledClient{}

type disabledClient struct{}

// IssueReturnLabel implements ports.LabelIssuer.
func (disabledClient) IssueReturnLabel(context.Context, requests.IssueReturnLabel) (requests.ReturnLabel, error) {
	return requests.ReturnLabel{}, ports.ErrLabelUnavailable
}
