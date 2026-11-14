package architecture

// PII sensor (wave-4 network design, C8 failure-mode table): no event, table,
// log line or wire contract of this context carries an address, a person's
// name, a phone number, an e-mail or free text. The ship-to stays in the
// shipping context (which issues the reverse label); `reason` is an enum, not
// a text field. The sensor walks the production sources, the migrations, the
// published wire contracts (encoder testdata) and the BDD features, and fails
// on the first PII-shaped identifier it meets. It deliberately does NOT forbid
// the word "address" in prose (a listen address or a broker address is not
// personal data) -- it forbids the identifier shapes a field would take:
// shipTo, addressLine, phoneNumber, recipientName, ... in CamelCase or
// snake_case, in Go code, SQL, JSON or YAML.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// piiDenylist is the identifier shapes personal data would take on the wire,
// in a struct or in a column. Each entry is matched case-sensitively in both
// its CamelCase and snake_case form.
var piiDenylist = []string{
	"shipTo", "ship_to",
	"shipToAddress", "ship_to_address",
	"addressLine", "address_line",
	"streetAddress", "street_address",
	"postalCode", "postal_code",
	"phoneNumber", "phone_number",
	"recipientName", "recipient_name",
	"customerName", "customer_name",
	"shopperName", "shopper_name",
	"personName", "person_name",
	"fullName", "full_name",
	"emailAddress", "email_address",
	"freeText", "free_text",
	"freeTextReason", "free_text_reason",
}

// piiScanRoots are the subtrees whose files carry the context's contracts:
// production code, migrations, the published wire goldens and the features.
var piiScanRoots = []string{
	filepath.Join("..", "..", "internal"),
	filepath.Join("..", "..", "cmd"),
	filepath.Join("..", "..", "features"),
}

// piiScanExt are the scanned file extensions.
var piiScanExt = []string{".go", ".sql", ".json", ".feature", ".yaml"}

func piiViolation(file string, token string) string {
	return fmt.Sprintf("ARCHITECTURE VIOLATION (PII rule)\n"+
		"  RULE:    no address, name, phone, e-mail or free text ever enters this context\n"+
		"  WHAT:    %s contains the PII-shaped identifier %q\n"+
		"  WHY:     the wave-4 design is explicit: the ship-to is the shipping context's fact\n"+
		"           (it issues the reverse label), reason is an enum, and no CR event, table,\n"+
		"           log or tool answer may carry personal data.\n"+
		"  FIX:     drop the field; if a sibling context needs it, it must read it from the\n"+
		"           context that owns it. Do NOT loosen this test.\n",
		file, token)
}

// TestNoPIIShapedIdentifiersAnywhere fails on the first denylist hit.
func TestNoPIIShapedIdentifiersAnywhere(t *testing.T) {
	hits := []string{}
	for _, root := range piiScanRoots {
		err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				return nil
			}
			if strings.HasSuffix(p, "_test.go") {
				// Test fixtures may quote a sibling's contract to prove it is
				// clean; the production surface is what must stay PII-free.
				return nil
			}
			scanned := false
			for _, ext := range piiScanExt {
				if strings.HasSuffix(p, ext) {
					scanned = true
					break
				}
			}
			if !scanned {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			body := string(raw)
			for _, token := range piiDenylist {
				if strings.Contains(body, token) {
					hits = append(hits, piiViolation(p, token))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(hits) > 0 {
		t.Fatalf("the PII sensor found %d violation(s):\n%s", len(hits), strings.Join(hits, ""))
	}
}

// TestReasonStaysAnEnum pins the other half of the PII rule: the return reason
// is a CHECK-constrained enum in the schema, never a free-text column.
func TestReasonStaysAnEnum(t *testing.T) {
	migrations := filepath.Join("..", "..", "internal", "adapters", "outbound", "postgres", "migrations")
	raw, err := os.ReadFile(filepath.Join(migrations, "0001_customer_returns_schema.up.sql"))
	if err != nil {
		t.Skipf("no schema migration found: %v", err)
	}
	schema := string(raw)
	if !strings.Contains(schema, "reason") {
		t.Fatal("the schema has no reason column to check")
	}
	if !strings.Contains(schema, "CHECK (reason IN ('DEFECTIVE', 'NOT_AS_DESCRIBED', 'WRONG_ITEM', 'NOT_NEEDED', 'OTHER'))") {
		t.Fatal("reason must stay a CHECK-constrained enum (DEFECTIVE, NOT_AS_DESCRIBED, WRONG_ITEM, NOT_NEEDED, OTHER); free text is a PII leak")
	}
}
