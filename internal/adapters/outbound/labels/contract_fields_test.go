package labels

// R4 of the consumer contract tests of POST /return-labels
// (contract_pin_test.go has R1-R3): every field the client's wire structs
// carry is declared by the pinned schema with a compatible type, and a field
// the pinned schema does not require is one the client tolerates the absence
// of. In the package itself because the wire structs are unexported.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/IQVO/customer-returns/internal/application/requests"
)

// calledOperation finds in the pinned spec the operation the real client
// calls, by the method and path the client put on the wire.
func calledOperation(t *testing.T) *openapi3.Operation {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile("../../../../contracts/shipping-and-tracking.openapi.yaml")
	if err != nil {
		t.Fatalf("the pinned shipping-and-tracking spec does not load: %v", err)
	}
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	_, _ = NewClient(srv.URL, srv.Client()).IssueReturnLabel(context.Background(), requests.IssueReturnLabel{
		ReturnAuthorizationID: "ret-7", OrderID: "ord-1", PackageNo: 2, DestinationNodeID: "node-rc-1",
	})
	item := doc.Paths.Find(path)
	if item == nil || item.GetOperation(method) == nil {
		t.Fatalf("the client calls %s %s, which the pinned spec does not declare", method, path)
	}
	return item.GetOperation(method)
}

// jsonSchemaOf is the application/json schema of a request or response body.
func jsonSchemaOf(t *testing.T, content openapi3.Content) *openapi3.Schema {
	t.Helper()
	media := content.Get("application/json")
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		t.Fatal("the pinned spec declares no application/json schema here")
	}
	return media.Schema.Value
}

// scalarWireTypes maps the scalar Go kinds a wire struct may carry to the
// OpenAPI type that must declare them.
var scalarWireTypes = map[reflect.Kind]string{
	reflect.String: "string", reflect.Bool: "boolean",
	reflect.Int: "integer", reflect.Int32: "integer", reflect.Int64: "integer",
	reflect.Float64: "number",
}

// zeroValueDefaults lists the fields whose Go zero value is this context's
// documented reading of an absent optional property. Empty today: both
// properties the client reads are required by the pinned schema.
var zeroValueDefaults = map[string]string{}

// toleratesAbsence: a pointer, a slice, a map, an omitempty field or a
// documented zero-value default survives a property the producer omitted.
func toleratesAbsence(f reflect.StructField, path string) bool {
	if _, documented := zeroValueDefaults[path]; documented {
		return true
	}
	if strings.HasSuffix(f.Tag.Get("json"), ",omitempty") {
		return true
	}
	switch f.Type.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		return true
	}
	return false
}

// checkWireFields asserts every JSON field of a wire struct against a pinned
// object schema. reads says the struct DECODES the message: a property the
// schema does not require must then tolerate absence.
func checkWireFields(t *testing.T, wire reflect.Type, schema *openapi3.Schema, name string, reads bool) {
	t.Helper()
	if wire.NumField() == 0 {
		t.Fatalf("%s: the wire struct has no field, nothing was checked", name)
	}
	for i := range wire.NumField() {
		checkWireField(t, wire.Field(i), schema, name, reads)
	}
}

func checkWireField(t *testing.T, f reflect.StructField, schema *openapi3.Schema, name string, reads bool) {
	t.Helper()
	tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if tag == "" || tag == "-" {
		t.Errorf("%s.%s has no JSON name: it is matched by Go's case-insensitive fallback, not by the contract", name, f.Name)
		return
	}
	path := name + "." + tag
	property := schema.Properties[tag]
	if property == nil || property.Value == nil {
		t.Errorf("%s: the client carries it, the pinned schema does not declare it", path)
		return
	}
	kind := f.Type.Kind()
	if kind == reflect.Pointer {
		kind = f.Type.Elem().Kind()
	}
	want, scalar := scalarWireTypes[kind]
	if !scalar {
		t.Errorf("%s: Go kind %s has no wire mapping in this test; extend it before adding such a field", path, kind)
		return
	}
	declared := property.Value.Type
	// A Go float64 reads any JSON number, so a schema integer satisfies it.
	readsInteger := reads && want == "number" && declared.Includes("integer")
	if !declared.Includes(want) && !readsInteger {
		t.Errorf("%s: the pinned schema says %v, the Go field is a %s", path, declared.Slice(), want)
	}
	if reads && !slices.Contains(schema.Required, tag) && !toleratesAbsence(f, path) {
		t.Errorf("%s: optional in the pinned schema, so the client must tolerate its absence (pointer, omitempty or a documented zero-value default)", path)
	}
}

// deprecatedSent lists the JSON fields of a wire struct the client SENDS that
// the pinned schema deprecates. A property the producer deprecated is one it
// will remove: the client must have stopped sending it before that day, not
// on it.
func deprecatedSent(wire reflect.Type, schema *openapi3.Schema) []string {
	var found []string
	for i := range wire.NumField() {
		tag, _, _ := strings.Cut(wire.Field(i).Tag.Get("json"), ",")
		if property := schema.Properties[tag]; property != nil && property.Value != nil && property.Value.Deprecated {
			found = append(found, tag)
		}
	}
	return found
}

// The deprecation check of R4 bites: a request struct that still carried the
// caller-supplied prefix, which the pinned route deprecates, is reported, and
// the node id beside it is not.
func TestADeprecatedRequestPropertyIsReported(t *testing.T) {
	op := calledOperation(t)
	body := jsonSchemaOf(t, op.RequestBody.Value.Content)
	type bothForms struct {
		NodeID string `json:"destinationNodeId"`
		Prefix string `json:"destinationPostalPrefix"`
	}
	if got := deprecatedSent(reflect.TypeOf(bothForms{}), body); !slices.Equal(got, []string{"destinationPostalPrefix"}) {
		t.Fatalf("deprecated properties reported = %v, want only destinationPostalPrefix", got)
	}
}

// R4.
func TestFieldsWeReadAreSpecified(t *testing.T) {
	op := calledOperation(t)
	created := op.Responses.Status(http.StatusCreated)
	if created == nil || created.Value == nil {
		t.Fatal("the pinned spec no longer answers 201 on the route: the client accepts nothing else")
	}
	checkWireFields(t, reflect.TypeOf(response{}), jsonSchemaOf(t, created.Value.Content), "ReturnLabel", true)

	if op.RequestBody == nil || op.RequestBody.Value == nil {
		t.Fatal("the pinned spec declares no request body on the route, the client sends one")
	}
	body := jsonSchemaOf(t, op.RequestBody.Value.Content)
	checkWireFields(t, reflect.TypeOf(request{}), body, "IssueReturnLabelRequest", false)
	for _, tag := range deprecatedSent(reflect.TypeOf(request{}), body) {
		t.Errorf("IssueReturnLabelRequest.%s: the pinned schema deprecates it and the client still sends it", tag)
	}
	// The client sends every property the producer requires.
	sends := map[string]bool{}
	for i := range reflect.TypeOf(request{}).NumField() {
		tag, _, _ := strings.Cut(reflect.TypeOf(request{}).Field(i).Tag.Get("json"), ",")
		sends[tag] = true
	}
	for _, r := range body.Required {
		if !sends[r] {
			t.Errorf("the route requires %q in the request, the client's wire struct has no such field", r)
		}
	}
}
