package kafka

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// Every type the projector dispatches on must be a type apis/asyncapi.yaml
// publishes on this context's own topic (the prefix and the entity are never
// invented here), and together the projected and acknowledged types are exactly
// the four published ones.
func TestAnalyticsTypesAreThePublishedOnesOfTheAsyncAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../../apis/asyncapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	var got []string
	for typ := range projectedKinds() {
		got = append(got, typ)
	}
	for typ := range acknowledgedTypes() {
		got = append(got, typ)
	}
	sort.Strings(got)
	if len(got) != 4 {
		t.Fatalf("projected+acknowledged types = %v, want the four published ones", got)
	}
	for _, typ := range got {
		if !strings.Contains(spec, "type: "+typ) {
			t.Errorf("%s is not an example type of apis/asyncapi.yaml", typ)
		}
		if !strings.HasPrefix(typ, "com.retailnetwork.commerce.customer-returns.returnauthorization.") {
			t.Errorf("%s does not carry the repo's own event-type prefix", typ)
		}
	}
}
