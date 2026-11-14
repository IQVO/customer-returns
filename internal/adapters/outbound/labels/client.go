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

// Client is the ports.LabelIssuer over the shipping context's future
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

type request struct {
	ReturnAuthorizationID   string `json:"returnAuthorizationId"`
	OrderID                 string `json:"orderId"`
	PackageNo               int    `json:"packageNo"`
	DestinationPostalPrefix string `json:"destinationPostalPrefix"`
}

type response struct {
	TrackingNumber string `json:"trackingNumber"`
	Carrier        string `json:"carrier"`
}

// IssueReturnLabel implements ports.LabelIssuer. A disabled client, a route
// that is not live yet, or any transport failure maps to
// ports.ErrLabelUnavailable: never a 500, never a retry storm.
func (c *Client) IssueReturnLabel(ctx context.Context, in requests.IssueReturnLabel) (requests.ReturnLabel, error) {
	if c.base == "" {
		return requests.ReturnLabel{}, ports.ErrLabelUnavailable
	}
	body, err := json.Marshal(request{
		ReturnAuthorizationID:   string(in.ReturnAuthorizationID),
		OrderID:                 in.OrderID,
		PackageNo:               in.PackageNo,
		DestinationPostalPrefix: in.DestinationPostalPrefix,
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
	// packageNo); the key restates that pair so a retry is a replay.
	req.Header.Set("Idempotency-Key", fmt.Sprintf("%s:%d", in.ReturnAuthorizationID, in.PackageNo))
	resp, err := c.http.Do(req)
	if err != nil {
		return requests.ReturnLabel{}, ports.ErrLabelUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
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
