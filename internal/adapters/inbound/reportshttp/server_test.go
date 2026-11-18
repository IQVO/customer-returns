package reportshttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/customer-returns/internal/adapters/inbound/reportshttp"
	"github.com/IQVO/customer-returns/internal/domain/returnsperformance"
)

// fakeReader answers fixed rows and records the range it was asked for.
type fakeReader struct {
	authorized []returnsperformance.AuthorizedDay
	received   []returnsperformance.ReceivedDay
	reasons    []returnsperformance.ReasonCount
	last       *time.Time
	err        error
	asked      returnsperformance.Range
	failOn     string // "" = every method fails with err; else only that method
}

func (f *fakeReader) fail(method string) error {
	if f.failOn == "" || f.failOn == method {
		return f.err
	}
	return nil
}

func (f *fakeReader) AuthorizedPerDay(_ context.Context, r returnsperformance.Range) ([]returnsperformance.AuthorizedDay, error) {
	f.asked = r
	return f.authorized, f.fail("authorized")
}

func (f *fakeReader) ReceivedPerDay(_ context.Context, r returnsperformance.Range) ([]returnsperformance.ReceivedDay, error) {
	f.asked = r
	return f.received, f.fail("received")
}

func (f *fakeReader) ReasonCounts(_ context.Context, r returnsperformance.Range) ([]returnsperformance.ReasonCount, error) {
	f.asked = r
	return f.reasons, f.fail("reasons")
}

func (f *fakeReader) LastEventAt(context.Context) (*time.Time, error) { return f.last, f.fail("last") }

type fakePing struct{ err error }

func (p fakePing) Ping(context.Context) error { return p.err }

var (
	now  = time.Date(2026, 11, 8, 18, 30, 0, 0, time.UTC)
	nov3 = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
)

func serve(t *testing.T, s *reportshttp.Server, method, target string) (int, string, map[string]any) {
	t.Helper()
	if s.Now == nil {
		s.Now = func() time.Time { return now }
	}
	h := reportshttp.NewRouter(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, target, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s: body %q is not JSON: %v", method, target, rec.Body.String(), err)
	}
	return rec.Code, rec.Header().Get("Content-Type"), body
}

func ptr(v float64) *float64 { return &v }

func TestReturnsPerformanceReport(t *testing.T) {
	asOf := now.Add(-90 * time.Second)
	r := &fakeReader{
		authorized: []returnsperformance.AuthorizedDay{{Day: nov3, Count: 3}},
		received: []returnsperformance.ReceivedDay{
			{Day: nov3, Received: 2, Samples: 1, P50: ptr(86400), P95: ptr(86400)},
			{Day: nov3.Add(24 * time.Hour), Received: 1},
		},
		reasons: []returnsperformance.ReasonCount{{Reason: "OTHER", Count: 1}, {Reason: "DEFECTIVE", Count: 2}},
		last:    &asOf,
	}
	code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/returns-performance?from=2026-11-01&to=2026-11-07")
	if code != http.StatusOK || ct != "application/json" {
		t.Fatalf("status %d content-type %s", code, ct)
	}
	want := map[string]any{
		"from": "2026-11-01", "to": "2026-11-07",
		"days": []any{
			map[string]any{"day": "2026-11-03", "returnsAuthorized": 3.0, "returnsReceived": 2.0, "timeToReceiveSamples": 1.0,
				"timeToReceiveP50Seconds": 86400.0, "timeToReceiveP95Seconds": 86400.0},
			map[string]any{"day": "2026-11-04", "returnsAuthorized": 0.0, "returnsReceived": 1.0, "timeToReceiveSamples": 0.0,
				"timeToReceiveP50Seconds": nil, "timeToReceiveP95Seconds": nil},
		},
		"reasonPareto": []any{
			map[string]any{"reason": "DEFECTIVE", "returns": 2.0, "share": 2.0 / 3, "cumulativeShare": 2.0 / 3},
			map[string]any{"reason": "OTHER", "returns": 1.0, "share": 1.0 / 3, "cumulativeShare": 1.0},
		},
		"freshness": map[string]any{"asOf": "2026-11-08T18:28:30Z", "lagSeconds": 90.0},
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v\nwant %v", body, want)
	}
	if !r.asked.From.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || !r.asked.To.Equal(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("asked range = %+v, want [11-01, 11-08)", r.asked)
	}
}

func TestAnEmptyDefaultRangeAnswersEmptyArraysAndNullFreshness(t *testing.T) {
	code, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/returns-performance")
	if code != http.StatusOK || body["from"] != "2026-10-10" || body["to"] != "2026-11-08" {
		t.Fatalf("status %d body %v, want the 30 days ending today", code, body)
	}
	for _, key := range []string{"days", "reasonPareto"} {
		if v, ok := body[key].([]any); !ok || len(v) != 0 {
			t.Errorf("%s = %#v, want []", key, body[key])
		}
	}
	fresh, _ := body["freshness"].(map[string]any)
	if v, ok := fresh["asOf"]; !ok || v != nil || fresh["lagSeconds"] != nil {
		t.Fatalf("freshness before the first event = %v, want explicit nulls", fresh)
	}
}

func TestFreshness(t *testing.T) {
	_, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/freshness")
	if v, ok := body["asOf"]; !ok || v != nil || body["lagSeconds"] != nil {
		t.Fatalf("before the first event = %v, want explicit nulls", body)
	}
	asOf := now.Add(-90 * time.Second)
	_, _, body = serve(t, &reportshttp.Server{Reader: &fakeReader{last: &asOf}}, http.MethodGet, "/reports/freshness")
	if body["asOf"] != "2026-11-08T18:28:30Z" || body["lagSeconds"] != 90.0 {
		t.Fatalf("freshness = %v", body)
	}
}

func assertProblem(t *testing.T, code int, ct string, body map[string]any, wantCode int, wantType string) {
	t.Helper()
	if code != wantCode || ct != "application/problem+json" || body["type"] != wantType || body["status"] != float64(wantCode) {
		t.Fatalf("got %d %s %v, want %d problem %s", code, ct, body, wantCode, wantType)
	}
}

func TestInvalidRangeIsAProblem(t *testing.T) {
	for _, q := range []string{"?from=2026-11-05&to=2026-11-04", "?from=nope", "?to=2026-13-01", "?from=2025-01-01&to=2026-11-01"} {
		code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/returns-performance"+q)
		assertProblem(t, code, ct, body, http.StatusBadRequest, "invalid-report-range")
	}
}

func TestEveryReaderFailureIsAnOpaque500(t *testing.T) {
	cause := errors.New("pq: password authentication failed for secret-role")
	for _, failOn := range []string{"authorized", "received", "reasons", "last"} {
		code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{err: cause, failOn: failOn}}, http.MethodGet, "/reports/returns-performance")
		assertProblem(t, code, ct, body, http.StatusInternalServerError, "internal-error")
		if strings.Contains(body["detail"].(string), "secret") {
			t.Fatalf("%s leaked the cause: %v", failOn, body)
		}
	}
	code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{err: cause}}, http.MethodGet, "/reports/freshness")
	assertProblem(t, code, ct, body, http.StatusInternalServerError, "internal-error")
}

func TestUnknownPathAndWriteMethodsAreProblems(t *testing.T) {
	code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/nope")
	assertProblem(t, code, ct, body, http.StatusNotFound, "not-found")
	code, ct, body = serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodPost, "/reports/freshness")
	assertProblem(t, code, ct, body, http.StatusMethodNotAllowed, "method-not-allowed")
}

func TestHealthAndReadiness(t *testing.T) {
	if code, _, body := serve(t, &reportshttp.Server{}, http.MethodGet, "/healthz"); code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("healthz = %d %v", code, body)
	}
	if code, _, body := serve(t, &reportshttp.Server{Ping: fakePing{}}, http.MethodGet, "/readyz"); code != http.StatusOK || body["status"] != "ready" {
		t.Fatalf("readyz = %d %v", code, body)
	}
	code, ct, body := serve(t, &reportshttp.Server{Ping: fakePing{err: errors.New("down")}}, http.MethodGet, "/readyz")
	assertProblem(t, code, ct, body, http.StatusServiceUnavailable, "dependency-unavailable")
	draining := &reportshttp.Server{}
	draining.SetNotReady()
	if code, _, body := serve(t, draining, http.MethodGet, "/readyz"); code != http.StatusServiceUnavailable || body["status"] != "not_ready" {
		t.Fatalf("draining readyz = %d %v", code, body)
	}
}
