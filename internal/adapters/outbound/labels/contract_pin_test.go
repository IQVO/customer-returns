package labels_test

// Consumer contract tests of the ONE in-org HTTP edge of this context: the
// shipping context's POST /return-labels (ADR 0005), against the pinned copy
// of IQVO/shipping-and-tracking's apis/openapi.yaml
// (contracts/shipping-and-tracking.openapi.yaml, registered in
// contracts/pinned.json and compared with the producer's develop on every CI
// run). The fleet's archetypes R1-R3 are here; R4 reflects over the client's
// unexported wire structs and lives in the package itself
// (contract_fields_test.go).
//
// The pinned spec carries NO example of this route: the answers served below
// are derived from the pinned schemas' required properties (producer example
// backfill pending).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/IQVO/customer-returns/internal/adapters/outbound/labels"
	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
)

// pinnedSpecPath is the pin, seen from this package. It lives at the repo
// root, outside internal/, on purpose: see contracts/pinned.json.
const pinnedSpecPath = "../../../../contracts/shipping-and-tracking.openapi.yaml"

func loadPinnedSpec(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile(pinnedSpecPath)
	if err != nil {
		t.Fatalf("the pinned shipping-and-tracking spec does not load: %v", err)
	}
	return doc
}

// sentRequest is what the real client put on the wire.
type sentRequest struct {
	method, path string
	header       http.Header
	body         []byte
}

// callRoute runs the REAL client against a stub that answers status,
// contentType and body, and returns the request the client sent with what it
// made of the answer.
func callRoute(t *testing.T, status int, contentType, body string) (sentRequest, requests.ReturnLabel, error) {
	t.Helper()
	var sent sentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read the client's request: %v", err)
		}
		sent = sentRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: raw}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	label, err := labels.NewClient(srv.URL, srv.Client()).IssueReturnLabel(context.Background(), labelRequest)
	return sent, label, err
}

// calledOperation finds in the pinned spec the operation the real client
// calls. The method and the path are the ones the client put on the wire,
// not a pair restated in this test.
func calledOperation(t *testing.T, doc *openapi3.T) (sentRequest, *openapi3.PathItem, *openapi3.Operation) {
	t.Helper()
	sent, _, _ := callRoute(t, http.StatusServiceUnavailable, "application/problem+json", "{}")
	item := doc.Paths.Find(sent.path)
	if item == nil {
		t.Fatalf("the client calls %s %s, a path the pinned spec does not have", sent.method, sent.path)
	}
	op := item.GetOperation(sent.method)
	if op == nil {
		t.Fatalf("the client calls %s %s, the pinned spec has no %s on that path", sent.method, sent.path, sent.method)
	}
	return sent, item, op
}

// bodySchema is the schema of one media type of a request or response body.
func bodySchema(t *testing.T, content openapi3.Content, mediaType string) *openapi3.Schema {
	t.Helper()
	media := content.Get(mediaType)
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		t.Fatalf("the pinned spec declares no %s schema here", mediaType)
	}
	return media.Schema.Value
}

// issuedSchema is the schema of the route's one success, the 201.
func issuedSchema(t *testing.T, op *openapi3.Operation) *openapi3.Schema {
	t.Helper()
	created := op.Responses.Status(http.StatusCreated)
	if created == nil || created.Value == nil {
		t.Fatal("the pinned spec no longer answers 201 on the route: the client accepts nothing else")
	}
	return bodySchema(t, created.Value.Content, "application/json")
}

// derive builds a value that satisfies a schema from its REQUIRED parts only:
// the first value of an enum, an instant for a date-time, a named placeholder
// for a free string, 1 (or the minimum) for a number.
func derive(t *testing.T, name string, s *openapi3.Schema) any {
	t.Helper()
	switch {
	case len(s.Enum) > 0:
		return s.Enum[0]
	case s.Type.Is("object"):
		out := map[string]any{}
		for _, required := range s.Required {
			property := s.Properties[required]
			if property == nil || property.Value == nil {
				t.Fatalf("%s requires %q and does not declare it", name, required)
			}
			out[required] = derive(t, required, property.Value)
		}
		return out
	case s.Type.Is("array") && s.Items != nil && s.Items.Value != nil:
		return []any{derive(t, name, s.Items.Value)}
	}
	return deriveScalar(t, name, s)
}

func deriveScalar(t *testing.T, name string, s *openapi3.Schema) any {
	t.Helper()
	switch {
	case s.Type.Is("integer"), s.Type.Is("number"):
		if s.Min != nil && *s.Min > 1 {
			return *s.Min
		}
		return 1
	case s.Type.Is("boolean"):
		return true
	case s.Type.Is("string") && s.Format == "date-time":
		return "2026-11-16T12:00:00Z"
	case s.Type.Is("string"):
		return "derived-" + name
	}
	t.Fatalf("cannot derive %q from a schema of type %v", name, s.Type)
	return nil
}

// derivedBody renders a derived value as the JSON body a stub serves.
func derivedBody(t *testing.T, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// satisfies validates a JSON body against a pinned schema.
func satisfies(t *testing.T, s *openapi3.Schema, body []byte) error {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	return s.VisitJSON(v)
}

// requestCallSites counts the places the production code of this package
// builds an HTTP request.
func requestCallSites(t *testing.T) int {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		n += strings.Count(string(raw), "http.NewRequest")
	}
	return n
}

// R1: the pinned spec covers every call site of the label client: the method
// and path it calls, the headers the route requires (declared on the path
// item or on the operation) and the request body it sends.
func TestPinnedSpecCoversEveryCallSite(t *testing.T) {
	doc := loadPinnedSpec(t)
	if n := requestCallSites(t); n != 1 {
		t.Fatalf("the label client builds %d HTTP requests: this test covers the one of IssueReturnLabel, cover the others", n)
	}
	sent, item, op := calledOperation(t, doc)

	declared := map[string]bool{}
	for _, parameter := range append(append(openapi3.Parameters{}, item.Parameters...), op.Parameters...) {
		p := parameter.Value
		if p == nil || p.In != "header" {
			continue
		}
		declared[p.Name] = p.Required
		value := sent.header.Get(p.Name)
		if p.Required && value == "" {
			t.Errorf("the route requires the header %s, the client does not send it", p.Name)
		}
		if value != "" && p.Schema != nil && p.Schema.Value != nil {
			if err := p.Schema.Value.VisitJSON(value); err != nil {
				t.Errorf("the client's %s %q does not satisfy the pinned parameter schema: %v", p.Name, value, err)
			}
		}
	}
	if !declared["Idempotency-Key"] {
		t.Errorf("the pinned spec no longer requires Idempotency-Key on %s %s: the client's replay safety rests on it (declared headers: %v)", sent.method, sent.path, declared)
	}

	if op.RequestBody == nil || op.RequestBody.Value == nil {
		t.Fatalf("the pinned spec declares no request body on %s %s, the client sends one", sent.method, sent.path)
	}
	contentType := sent.header.Get("Content-Type")
	if err := satisfies(t, bodySchema(t, op.RequestBody.Value.Content, contentType), sent.body); err != nil {
		t.Errorf("the request the client sends does not satisfy the pinned request schema: %v\n%s", err, sent.body)
	}
}

// R2: the answers the client's tests rely on satisfy the pinned schema of the
// 201: the hand-written fixture of client_test.go and the one derived here.
func TestExamplesSatisfyPinnedSpec(t *testing.T) {
	_, _, op := calledOperation(t, loadPinnedSpec(t))
	schema := issuedSchema(t, op)
	fixtures := []struct{ name, body string }{
		{"the fixture of the client's own tests", returnLabelBody},
		{"derived from the required properties of the pinned schema", derivedBody(t, derive(t, "ReturnLabel", schema))},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			if err := satisfies(t, schema, []byte(fixture.body)); err != nil {
				t.Errorf("the fixture does not satisfy the pinned schema of the 201: %v\n%s", err, fixture.body)
			}
		})
	}
}

// R3: the real client decodes a spec-shaped 201 into the label the port
// returns, and maps every error status the pinned spec documents for the
// route to the port's sentinel, ports.ErrLabelUnavailable.
func TestGatewayDecodesSpecExamples(t *testing.T) {
	_, _, op := calledOperation(t, loadPinnedSpec(t))

	t.Run("the 201, with the required properties only", func(t *testing.T) { assertIssuedDecodes(t, op) })

	documented := 0
	for code, response := range op.Responses.Map() {
		status, err := strconv.Atoi(code)
		if err != nil || status/100 == 2 || response.Value == nil {
			continue
		}
		documented++
		t.Run("the documented "+code, func(t *testing.T) { assertProblemIsUnavailable(t, status, response.Value) })
	}
	if documented == 0 {
		t.Fatal("the pinned spec documents no error status on the route: nothing was proven about the sentinel")
	}
}

func assertIssuedDecodes(t *testing.T, op *openapi3.Operation) {
	t.Helper()
	derived, ok := derive(t, "ReturnLabel", issuedSchema(t, op)).(map[string]any)
	if !ok {
		t.Fatal("the pinned schema of the 201 is not an object")
	}
	_, label, err := callRoute(t, http.StatusCreated, "application/json", derivedBody(t, derived))
	if err != nil {
		t.Fatalf("a spec-shaped 201 is refused: %v", err)
	}
	want := requests.ReturnLabel{TrackingNumber: asText(derived["trackingNumber"]), Carrier: asText(derived["carrier"])}
	if label != want || want.TrackingNumber == "" || want.Carrier == "" {
		t.Fatalf("label = %+v, want %+v", label, want)
	}
}

func asText(v any) string {
	s, _ := v.(string)
	return s
}

func assertProblemIsUnavailable(t *testing.T, status int, response *openapi3.Response) {
	t.Helper()
	schema := bodySchema(t, response.Content, "application/problem+json")
	problem, ok := derive(t, "Problem", schema).(map[string]any)
	if !ok {
		t.Fatal("the pinned problem schema is not an object")
	}
	problem["status"] = status
	body := derivedBody(t, problem)
	if err := satisfies(t, schema, []byte(body)); err != nil {
		t.Fatalf("the derived problem does not satisfy the pinned schema: %v", err)
	}
	_, label, err := callRoute(t, status, "application/problem+json", body)
	if !errors.Is(err, ports.ErrLabelUnavailable) || label != (requests.ReturnLabel{}) {
		t.Fatalf("a documented %d answered %+v, %v: want ports.ErrLabelUnavailable", status, label, err)
	}
}
