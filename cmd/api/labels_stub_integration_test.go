//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// labelStubCall is one request the stubbed route received with a readable
// body: how it named the return's destination, and its Idempotency-Key.
type labelStubCall struct {
	Key       string
	PackageNo int
	NodeID    *string // destinationNodeId, nil when absent
	Prefix    *string // destinationPostalPrefix (deprecated), nil when absent
}

// labelStub plays the shipping context's POST /return-labels as its contract
// stands (shipping-and-tracking apis/openapi.yaml and its ADR 0018; the copy
// pinned in contracts/):
//
//   - the return's destination is named by EXACTLY ONE of destinationNodeId
//     and the deprecated destinationPostalPrefix; both, neither, an empty
//     one, an unknown property or a missing required one is 422
//     validation-failed;
//   - no Idempotency-Key is 400 idempotency-key-required;
//   - package 3 is the outage case of the e2e (503 ship-to-unavailable);
//   - everything else is 201 Created with a ReturnLabel, every required
//     property of that schema.
//
// It records what it was sent, so a test can assert what left this context.
type labelStub struct {
	*httptest.Server
	mu    sync.Mutex
	calls []labelStubCall
}

// sent returns the recorded requests in arrival order.
func (s *labelStub) sent() []labelStubCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]labelStubCall(nil), s.calls...)
}

func stubProblem(w http.ResponseWriter, status int, slug string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": slug, "title": slug, "status": status})
}

func stubShippingLabels(t *testing.T) *labelStub {
	t.Helper()
	stub := &labelStub{}
	stub.Server = httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(stub.Close)
	return stub
}

func (s *labelStub) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/return-labels" || r.Method != http.MethodPost {
		stubProblem(w, http.StatusNotFound, "not-found")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		stubProblem(w, http.StatusBadRequest, "idempotency-key-required")
		return
	}
	var body struct {
		ReturnAuthorizationID string  `json:"returnAuthorizationId"`
		OrderID               string  `json:"orderId"`
		PackageNo             int     `json:"packageNo"`
		NodeID                *string `json:"destinationNodeId"`
		Prefix                *string `json:"destinationPostalPrefix"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields() // additionalProperties: false
	if err := decoder.Decode(&body); err != nil || body.ReturnAuthorizationID == "" || body.OrderID == "" || body.PackageNo < 1 {
		stubProblem(w, http.StatusUnprocessableEntity, "validation-failed")
		return
	}
	s.mu.Lock()
	s.calls = append(s.calls, labelStubCall{Key: key, PackageNo: body.PackageNo, NodeID: body.NodeID, Prefix: body.Prefix})
	s.mu.Unlock()
	// Exactly one of the two names of the destination, and not an empty one.
	named := body.NodeID
	if named == nil {
		named = body.Prefix
	}
	if (body.NodeID == nil) == (body.Prefix == nil) || *named == "" {
		stubProblem(w, http.StatusUnprocessableEntity, "validation-failed")
		return
	}
	if body.PackageNo == 3 {
		stubProblem(w, http.StatusServiceUnavailable, "ship-to-unavailable")
		return
	}
	tracking := fmt.Sprintf("RN-RET-%s-%d", strings.TrimPrefix(body.ReturnAuthorizationID, "ret-"), body.PackageNo)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"labelRef":              "lbl-" + tracking,
		"trackingNumber":        tracking,
		"carrier":               "RN-GROUND",
		"packageNo":             body.PackageNo,
		"orderId":               body.OrderID,
		"tier":                  "STANDARD",
		"status":                "ISSUED",
		"issuedAt":              "2026-11-16T12:00:00Z",
		"purpose":               "RETURN",
		"returnAuthorizationId": body.ReturnAuthorizationID,
	})
}
