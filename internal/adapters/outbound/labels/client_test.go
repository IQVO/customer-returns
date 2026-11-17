package labels_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IQVO/customer-returns/internal/adapters/outbound/labels"
	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
)

// returnLabelBody is an answer of the shipping context's POST /return-labels
// shaped like its ReturnLabel schema: every required property (labelRef,
// trackingNumber, carrier, packageNo, orderId, tier, status, issuedAt, purpose,
// returnAuthorizationId) and nothing the schema does not declare.
const returnLabelBody = `{"labelRef":"lbl-7","trackingNumber":"RN-RET-7-2","carrier":"RN-GROUND",` +
	`"packageNo":2,"orderId":"ord-1","tier":"STANDARD","status":"ISSUED","issuedAt":"2026-11-16T12:00:00Z",` +
	`"purpose":"RETURN","returnAuthorizationId":"ret-7"}`

var labelRequest = requests.IssueReturnLabel{
	ReturnAuthorizationID: "ret-7", OrderID: "ord-1", PackageNo: 2, DestinationPostalPrefix: "01310",
}

var wantLabel = requests.ReturnLabel{TrackingNumber: "RN-RET-7-2", Carrier: "RN-GROUND"}

// answer is one canned response of the stubbed route.
type answer struct {
	status      int
	contentType string
	header      map[string]string
	body        string
}

// route stubs the shipping context: it answers a and counts the calls.
func route(t *testing.T, a answer) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		for k, v := range a.header {
			w.Header().Set(k, v)
		}
		if a.contentType != "" {
			w.Header().Set("Content-Type", a.contentType)
		}
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func issue(t *testing.T, baseURL string) (requests.ReturnLabel, error) {
	t.Helper()
	return labels.NewClient(baseURL, nil).IssueReturnLabel(context.Background(), labelRequest)
}

// The route answers 201 Created with a ReturnLabel (the shipping context's
// apis/openapi.yaml, POST /return-labels): that is the success of this client.
func TestIssueReturnLabelAcceptsThe201TheRouteAnswers(t *testing.T) {
	srv, calls := route(t, answer{status: http.StatusCreated, contentType: "application/json", body: returnLabelBody})
	got, err := issue(t, srv.URL)
	if err != nil {
		t.Fatalf("a 201 with a ReturnLabel must succeed, got %v", err)
	}
	if got != wantLabel {
		t.Fatalf("label = %+v, want %+v", got, wantLabel)
	}
	if *calls != 1 {
		t.Fatalf("calls = %d, want exactly 1 (no retry)", *calls)
	}
}

// A replay of the same Idempotency-Key "returns the first response": the same
// 201 and body, plus Idempotency-Replayed: true. It is the same success.
func TestIssueReturnLabelAcceptsAReplayedFirstResponse(t *testing.T) {
	srv, _ := route(t, answer{
		status: http.StatusCreated, contentType: "application/json", body: returnLabelBody,
		header: map[string]string{"Idempotency-Replayed": "true"},
	})
	got, err := issue(t, srv.URL)
	if err != nil {
		t.Fatalf("a replayed 201 must succeed, got %v", err)
	}
	if got != wantLabel {
		t.Fatalf("label = %+v, want %+v", got, wantLabel)
	}
}

// The contract defines 201 as the ONLY success of the route: a replay answers
// the first response again (201), never 200. Every other status, the 2xx
// neighbours of 201 included, is label-unavailable even with a label body.
func TestIssueReturnLabelRejectsEveryStatusButThe201(t *testing.T) {
	for _, status := range []int{
		http.StatusOK, http.StatusAccepted, http.StatusNonAuthoritativeInfo, http.StatusNoContent,
		http.StatusMultipleChoices, http.StatusNotModified,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv, calls := route(t, answer{status: status, contentType: "application/json", body: returnLabelBody})
			got, err := issue(t, srv.URL)
			assertUnavailable(t, got, err)
			if *calls != 1 {
				t.Fatalf("calls = %d, want exactly 1 (no retry storm)", *calls)
			}
		})
	}
}

// The error statuses the contract documents all stay label-unavailable (ADR
// 0005: tolerant, never a 500, never a retry).
func TestIssueReturnLabelMapsEveryProviderErrorToUnavailable(t *testing.T) {
	cases := []struct {
		name   string
		status int
		slug   string
	}{
		{"idempotency key required", http.StatusBadRequest, "idempotency-key-required"},
		{"label already issued", http.StatusConflict, "label-already-issued"},
		{"tracking number taken", http.StatusConflict, "tracking-number-taken"},
		{"concurrent update", http.StatusConflict, "concurrent-update"},
		{"idempotency key reused", http.StatusConflict, "idempotency-key-reused"},
		{"validation failed", http.StatusUnprocessableEntity, "validation-failed"},
		{"internal error", http.StatusInternalServerError, "internal-error"},
		{"ship-to unavailable", http.StatusServiceUnavailable, "ship-to-unavailable"},
		{"route not deployed", http.StatusNotFound, "not-found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, calls := route(t, answer{
				status: c.status, contentType: "application/problem+json", body: problem(c.slug, c.status),
			})
			got, err := issue(t, srv.URL)
			assertUnavailable(t, got, err)
			if *calls != 1 {
				t.Fatalf("calls = %d, want exactly 1 (no retry storm)", *calls)
			}
		})
	}
}

// A 201 the client cannot read a tracking number from is not a label.
func TestIssueReturnLabelRejectsA201WithoutATrackingNumber(t *testing.T) {
	cases := map[string]string{
		"not JSON":              `<html>`,
		"empty body":            ``,
		"no tracking number":    `{"labelRef":"lbl-7","carrier":"RN-GROUND"}`,
		"empty tracking number": `{"labelRef":"lbl-7","trackingNumber":"","carrier":"RN-GROUND"}`,
		"a problem body":        problem("internal-error", http.StatusInternalServerError),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := route(t, answer{status: http.StatusCreated, contentType: "application/json", body: body})
			got, err := issue(t, srv.URL)
			assertUnavailable(t, got, err)
		})
	}
}

func TestIssueReturnLabelMapsATransportFailureToUnavailable(t *testing.T) {
	srv, _ := route(t, answer{status: http.StatusCreated, body: returnLabelBody})
	url := srv.URL
	srv.Close()
	got, err := issue(t, url)
	assertUnavailable(t, got, err)
}

func TestIssueReturnLabelMapsACancelledContextToUnavailable(t *testing.T) {
	srv, calls := route(t, answer{status: http.StatusCreated, body: returnLabelBody})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := labels.NewClient(srv.URL, nil).IssueReturnLabel(ctx, labelRequest)
	assertUnavailable(t, got, err)
	if *calls != 0 {
		t.Fatalf("calls = %d, want 0 for a cancelled context", *calls)
	}
}

// With no base URL the client is disabled and never dials (ADR 0005).
func TestIssueReturnLabelIsDisabledWithoutABaseURL(t *testing.T) {
	for _, base := range []string{"", "   ", "\t\n"} {
		got, err := issue(t, base)
		assertUnavailable(t, got, err)
	}
	got, err := labels.Disabled.IssueReturnLabel(context.Background(), labelRequest)
	assertUnavailable(t, got, err)
}

// The request is the IssueReturnLabelRequest of the contract: exactly its four
// required properties (additionalProperties is false there) on POST
// /return-labels, with the Idempotency-Key the route requires.
func TestIssueReturnLabelSendsTheContractRequest(t *testing.T) {
	var (
		method, path string
		header       http.Header
		sent         map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, header = r.Method, r.URL.Path, r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(returnLabelBody))
	}))
	t.Cleanup(srv.Close)

	// A trailing slash and padding in the configured URL never reach the path.
	if _, err := issue(t, " "+srv.URL+"// "); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if method != http.MethodPost || path != "/return-labels" {
		t.Fatalf("called %s %s, want POST /return-labels", method, path)
	}
	want := map[string]any{
		"returnAuthorizationId": "ret-7", "orderId": "ord-1", "packageNo": float64(2), "destinationPostalPrefix": "01310",
	}
	if len(sent) != len(want) {
		t.Fatalf("body = %v, want exactly %v", sent, want)
	}
	for k, v := range want {
		if sent[k] != v {
			t.Errorf("body[%s] = %v, want %v", k, sent[k], v)
		}
	}
	for k, v := range map[string]string{
		"Idempotency-Key": "ret-7:2", "Content-Type": "application/json", "Accept": "application/json",
	} {
		if got := header.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
}

// problem is the RFC 9457 body the route answers on a failure.
func problem(slug string, status int) string {
	b, _ := json.Marshal(map[string]any{"type": slug, "title": slug, "status": status})
	return string(b)
}

func assertUnavailable(t *testing.T, got requests.ReturnLabel, err error) {
	t.Helper()
	if !errors.Is(err, ports.ErrLabelUnavailable) {
		t.Fatalf("err = %v, want ports.ErrLabelUnavailable", err)
	}
	if got != (requests.ReturnLabel{}) {
		t.Fatalf("label = %+v, want the zero label beside the error", got)
	}
}
